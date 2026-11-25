package closeout

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/labels"
)

type roles map[string]bool // "kind/name"

func (r roles) Role(k config.Kind, name string) (config.Role, bool) {
	role := config.Role{Kind: k, Name: name}
	if k == config.KindTriage {
		role.PlanSection = config.BuiltinPlanSections[name]
	}
	return role, r[string(k)+"/"+name]
}

var testRoles = roles{
	"triage/lead": true, "triage/architect": true, "triage/designer": true,
	"triage/engineer": true, "triage/product-manager": true,
	"worker/engineer":     true,
	"reviewer/eng-review": true, "reviewer/license-review": true, "reviewer/security-review": true,
}

func lbls(ls []Label) []string {
	var out []string
	for _, l := range ls {
		out = append(out, fmt.Sprintf("%d:%s", l.Number, l.Name))
	}
	return out
}

func TestBuild(t *testing.T) {
	lead := Job{Stage: StageTriage, Role: "lead", Issue: 7}
	worker := Job{Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9, Branch: "worker/engineer-x",
		Worker: &WorkerResult{PR: 9, Tests: TestsPassed}}
	review := Job{Stage: StageReview, Role: "eng-review", Issue: 7, PR: 9, Branch: "worker/engineer-x", Review: &ReviewContext{}}
	tests := []struct {
		name         string
		j            Job
		rf           RoleFile
		outcome      Outcome
		terminal     []string
		remove       []string
		requests     []string
		deferred     []string
		skippedCount int
	}{
		{"lead done", lead, RoleFile{Status: "done", Next: []string{"triage:architect", "review:license-review"}, Worker: "engineer"},
			OutcomeDone, []string{"7:ard:triage:lead:done"}, []string{"7:ard:pending", "7:ard:triage:lead:running"},
			[]string{"7:ard:triage:architect"}, []string{"review:license-review"}, 0},
		{"lead blocked keeps pending", lead, RoleFile{Status: "blocked", Next: []string{"triage:architect"}},
			OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:triage:lead:running"}, nil, nil, 0},
		{"lead needs-human", lead, RoleFile{Status: "needs-human"},
			OutcomeNeedsHuman, []string{"7:ard:needs-human"}, []string{"7:ard:triage:lead:running"}, nil, nil, 0},
		{"lead bad worker", lead, RoleFile{Status: "done", Worker: "ghost"},
			OutcomeDone, []string{"7:ard:triage:lead:done"}, []string{"7:ard:pending", "7:ard:triage:lead:running"}, nil, nil, 1},
		{"architect done", Job{Stage: StageTriage, Role: "architect", Issue: 7}, RoleFile{Status: "done"},
			OutcomeDone, []string{"7:ard:triage:architect:done"}, []string{"7:ard:triage:architect:running"}, nil, nil, 0},
		{"worker done requests reviewers on the PR", worker, RoleFile{Status: "done", Next: []string{"review:eng-review", "triage:architect"}},
			OutcomeDone, []string{"7:ard:worker:engineer:done"},
			[]string{"7:ard:tests:failed", "9:ard:tests:failed", "7:ard:worker:engineer:running"},
			[]string{"9:ard:review:eng-review"}, nil, 1},
		{"review approve", review, RoleFile{Status: "done", Verdict: "approve", Next: []string{"review:license-review"}},
			OutcomeDone, []string{"9:ard:review:eng-review:done"}, []string{"9:ard:review:eng-review:changes", "9:ard:review:eng-review:running"}, nil, nil, 1},
		{"review approve, no context (restart) is stale", Job{Stage: StageReview, Role: "eng-review", Issue: 7, PR: 9, Branch: "worker/engineer-x"}, RoleFile{Status: "done", Verdict: "approve"},
			OutcomeStale, nil, []string{"9:ard:review:eng-review:running"}, nil, nil, 0},
		{"review approve, head moved is stale", Job{Stage: StageReview, Role: "eng-review", Issue: 7, PR: 9, Branch: "worker/engineer-x", Review: &ReviewContext{HeadMoved: true}}, RoleFile{Status: "done", Verdict: "approve"},
			OutcomeStale, nil, []string{"9:ard:review:eng-review:running"}, nil, nil, 0},
		{"review changes", review, RoleFile{Status: "done", Verdict: "changes"},
			OutcomeChanges, []string{"9:ard:review:eng-review:changes"}, []string{"9:ard:review:eng-review:done", "9:ard:review:eng-review:running"}, nil, nil, 0},
		{"review blocked ignores verdict", review, RoleFile{Status: "blocked", Verdict: "changes"},
			OutcomeNeedsHuman, []string{"9:ard:needs-human"}, []string{"9:ard:review:eng-review:running"}, nil, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := Build(tc.j, tc.rf, testRoles)
			if p.Outcome != tc.outcome {
				t.Errorf("outcome = %s, want %s", p.Outcome, tc.outcome)
			}
			if got := lbls(p.Terminal); !reflect.DeepEqual(got, tc.terminal) {
				t.Errorf("terminal = %v, want %v", got, tc.terminal)
			}
			if got := lbls(p.Remove); !reflect.DeepEqual(got, tc.remove) {
				t.Errorf("remove = %v, want %v", got, tc.remove)
			}
			if got := lbls(p.Requests); !reflect.DeepEqual(got, tc.requests) {
				t.Errorf("requests = %v, want %v", got, tc.requests)
			}
			var def []string
			for _, d := range p.Deferred {
				def = append(def, d.String())
			}
			if !reflect.DeepEqual(def, tc.deferred) {
				t.Errorf("deferred = %v, want %v", def, tc.deferred)
			}
			if len(p.Skipped) != tc.skippedCount {
				t.Errorf("skipped = %v, want %d", p.Skipped, tc.skippedCount)
			}
			if last := p.Remove[len(p.Remove)-1].Name; last != labels.Running(labels.Stage(tc.j.Stage), tc.j.Role) {
				t.Errorf("last removal = %s, want running", last)
			}
		})
	}
	if p := Build(lead, RoleFile{Status: "done", Worker: "engineer"}, testRoles); p.Worker != "engineer" {
		t.Errorf("worker = %q", p.Worker)
	}
}

func TestResolveNext(t *testing.T) {
	lead := Job{Stage: StageTriage, Role: "lead", Issue: 7}
	architect := Job{Stage: StageTriage, Role: "architect", Issue: 7}
	worker := Job{Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9}
	workerNoPR := Job{Stage: StageWorker, Role: "engineer", Issue: 7}
	tests := []struct {
		name     string
		j        Job
		in       []string
		now      []string
		deferred []string
		skipped  map[string]string
	}{
		{"unknown role", lead, []string{"triage:ghost"}, nil, nil, map[string]string{"triage:ghost": "no such role"}},
		{"bad grammar", lead, []string{"nope", "worker:engineer", "scanner:x", "triage:Up", "triage:"}, nil, nil,
			map[string]string{"nope": "bad grammar", "worker:engineer": "bad grammar", "scanner:x": "bad grammar", "triage:Up": "bad grammar", "triage:": "bad grammar"}},
		{"self request", architect, []string{"triage:architect"}, nil, nil, map[string]string{"triage:architect": "self request"}},
		{"lead not requestable", architect, []string{"triage:lead"}, nil, nil, map[string]string{"triage:lead": "not requestable"}},
		{"duplicate", lead, []string{"triage:architect", "triage:architect"}, []string{"triage:architect"}, nil, map[string]string{"triage:architect": "duplicate"}},
		{"worker triage is wrong stage", worker, []string{"triage:architect"}, nil, nil, map[string]string{"triage:architect": "wrong stage"}},
		{"review requests nothing", Job{Stage: StageReview, Role: "eng-review", Issue: 7, PR: 9}, []string{"review:license-review"}, nil, nil, map[string]string{"review:license-review": "wrong stage"}},
		{"triage review deferred", lead, []string{"review:license-review", "review:ghost"}, nil, []string{"review:license-review"}, map[string]string{"review:ghost": "no such role"}},
		{"worker review no PR", workerNoPR, []string{"review:eng-review"}, nil, nil, map[string]string{"review:eng-review": "no PR yet"}},
		{"worker review", worker, []string{"review:eng-review"}, []string{"review:eng-review"}, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now, def, skipped := resolveNext(tc.j, tc.in, testRoles)
			var gn, gd []string
			for _, r := range now {
				gn = append(gn, r.String())
			}
			for _, r := range def {
				gd = append(gd, r.String())
			}
			if !reflect.DeepEqual(gn, tc.now) && !(len(gn) == 0 && len(tc.now) == 0) {
				t.Errorf("now = %v, want %v", gn, tc.now)
			}
			if !reflect.DeepEqual(gd, tc.deferred) && !(len(gd) == 0 && len(tc.deferred) == 0) {
				t.Errorf("deferred = %v, want %v", gd, tc.deferred)
			}
			got := map[string]string{}
			for _, s := range skipped {
				got[s.Entry] = s.Why
			}
			if !reflect.DeepEqual(got, tc.skipped) && !(len(got) == 0 && len(tc.skipped) == 0) {
				t.Errorf("skipped = %v, want %v", got, tc.skipped)
			}
		})
	}
}

func TestPlansNotApplyingNextOnFailure(t *testing.T) {
	j := Job{Stage: StageTriage, Role: "lead", Issue: 7}
	p := failedPlan(j, "role file missing", false)
	if len(p.Requests) != 0 || lbls(p.Terminal)[0] != "7:ard:executor:failed" || len(p.Terminal) != 1 {
		t.Errorf("failed plan = %+v", p)
	}
	p = failedPlan(j, "x", true)
	if got := lbls(p.Terminal); !reflect.DeepEqual(got, []string{"7:ard:executor:failed", "7:ard:needs-human"}) {
		t.Errorf("withheld failed terminal = %v", got)
	}
	p = failedPlan(Job{Stage: StageReview, Role: "r", Issue: 7, PR: 9}, "x", false)
	if lbls(p.Remove)[0] != "9:ard:review:r:running" {
		t.Errorf("review failure targets the PR: %v", lbls(p.Remove))
	}
}

func TestResolveNextLimit(t *testing.T) {
	rs := roles{}
	var in []string
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("a%d", i)
		rs["triage/"+name] = true
		in = append(in, "triage:"+name)
	}
	now, _, skipped := resolveNext(Job{Stage: StageTriage, Role: "lead", Issue: 7}, in, rs)
	if len(now) != 8 || len(skipped) != 1 || skipped[0].Why != "limit 8" {
		t.Fatalf("now = %v, skipped = %v", now, skipped)
	}
}

func TestBuildNeedsNetwork(t *testing.T) {
	rf := RoleFile{Status: StatusNeedsNetwork, Network: []NetworkNeed{{What: "w", URL: "https://example.com/a", Why: "y"}}}
	for name, w := range map[string]*WorkerResult{"with result": {PR: 9, Tests: TestsPassed}, "reaper, no result": nil} {
		t.Run(name, func(t *testing.T) {
			j := Job{Stage: StageWorker, Role: "engineer", Issue: 7, Worker: w}
			p := Build(j, rf, testRoles)
			if p.Outcome != OutcomeNeedsNetwork || len(p.Network) != 1 || p.Worker != "" || len(p.Requests) != 0 || len(p.Deferred) != 0 {
				t.Fatalf("plan %+v", p)
			}
			if !reflect.DeepEqual(lbls(p.Terminal), []string{"7:ard:needs-human"}) || !reflect.DeepEqual(lbls(p.Remove), []string{"7:ard:worker:engineer:running"}) {
				t.Errorf("labels %v %v", lbls(p.Terminal), lbls(p.Remove))
			}
		})
	}
	if p := Build(Job{Stage: StageTriage, Role: "lead", Issue: 7}, rf, testRoles); p.Outcome != OutcomeNeedsHuman || p.Network != nil {
		t.Errorf("triage: %+v", p)
	}
	done := Job{Stage: StageWorker, Role: "engineer", Issue: 7, PR: 9, Worker: &WorkerResult{PR: 9, Tests: TestsPassed}}
	if p := Build(done, RoleFile{Status: StatusDone}, testRoles); p.Outcome != OutcomeDone {
		t.Errorf("done: %q", p.Outcome)
	}
}
