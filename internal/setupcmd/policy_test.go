package setupcmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
)

var testRepo = github.Repo{Owner: "o", Name: "r"}

const appSlug = "ard-test" // the fake's identity is ard-test[bot]

var appBypass = github.BypassActor{ActorType: "Integration", ActorID: 77, Mode: "always"}

func goodRuleset() github.Ruleset {
	return github.Ruleset{ID: 1, Name: "main", Target: "branch", Enforcement: "active",
		Branches: []string{"~DEFAULT_BRANCH"}, Rules: []string{"deletion", "pull_request"}, Bypass: []github.BypassActor{appBypass}}
}

// policyFake returns a fake that satisfies the whole policy.
func policyFake() *fake.Fake {
	f := fake.New()
	f.SetApp(appSlug, github.AppInfo{ID: 77, Permissions: map[string]string{"contents": "write", "pull_requests": "write"}})
	f.SetRulesets(goodRuleset())
	return f
}

func policyDeps(f *fake.Fake) Deps {
	d := deps(f)
	d.Policy, d.Repo = f, testRepo
	return d
}

func row(t *testing.T, r Report, name string) Row {
	t.Helper()
	for _, x := range r.Rows {
		if x.Name == name {
			return x
		}
	}
	t.Fatalf("no %s row in %+v", name, r.Rows)
	return Row{}
}

func runCheck(t *testing.T, f *fake.Fake) Report {
	t.Helper()
	rep, err := Run(context.Background(), policyDeps(f), Opts{Check: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestPolicyAllOK(t *testing.T) {
	rep := runCheck(t, policyFake())
	for _, n := range []string{rulesetName, appPermName, mergeName} {
		if r := row(t, rep, n); r.Status != StatusOK {
			t.Errorf("%s = %+v", n, r)
		}
	}
}

func TestRulesetDrift(t *testing.T) {
	other := github.BypassActor{ActorType: "Team", ActorID: 5, Mode: "always"}
	pr := func(m string) github.BypassActor { a := appBypass; a.Mode = m; return a }
	cases := []struct {
		name   string
		mut    func(*github.Ruleset)
		rs     []github.Ruleset // replaces the single ruleset when set
		detail string
	}{
		{"no pull request", func(r *github.Ruleset) { r.Rules = []string{"deletion"} }, nil, "requires a pull request"},
		{"extra bypass", func(r *github.Ruleset) { r.Bypass = append(r.Bypass, other) }, nil, "unexpected bypass actor Team 5"},
		{"pull_request mode", func(r *github.Ruleset) { r.Bypass = []github.BypassActor{pr("pull_request")} }, nil, `bypass mode is "pull_request"`},
		{"app missing", func(r *github.Ruleset) { r.Bypass = nil }, nil, "not an \"always\" bypass actor"},
		{"other branch", func(r *github.Ruleset) { r.Branches = []string{"refs/heads/dev"} }, nil, "requires a pull request"},
		{"excluded", func(r *github.Ruleset) { r.Exclude = []string{"refs/heads/main"} }, nil, "requires a pull request"},
		{"not active", func(r *github.Ruleset) { r.Enforcement = "evaluate" }, nil, "requires a pull request"},
		{"none", nil, []github.Ruleset{}, "requires a pull request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := policyFake()
			rs := []github.Ruleset{goodRuleset()}
			if c.rs != nil {
				rs = c.rs
			} else {
				c.mut(&rs[0])
			}
			f.SetRulesets(rs...)
			r := row(t, runCheck(t, f), rulesetName)
			if r.Status != StatusDrift || !strings.Contains(r.Detail, c.detail) || !strings.Contains(r.Detail, "https://github.com/o/r/settings/rules") {
				t.Errorf("row = %+v", r)
			}
		})
	}
}

func TestRulesetsUnion(t *testing.T) {
	f := policyFake()
	a, b := goodRuleset(), goodRuleset()
	a.Rules, a.Bypass = []string{"pull_request"}, nil
	b.ID, b.Rules, b.Branches = 2, []string{"deletion"}, []string{"refs/heads/main"}
	f.SetRulesets(a, b)
	if r := row(t, runCheck(t, f), rulesetName); r.Status != StatusOK {
		t.Errorf("row = %+v", r)
	}
}

func TestRulesetForbiddenIsManual(t *testing.T) {
	f := policyFake()
	f.FailNext("ListRulesets", github.ErrForbidden)
	var out bytes.Buffer
	rep, err := Run(context.Background(), policyDeps(f), Opts{Check: true}, &out)
	if err != nil {
		t.Fatal(err)
	}
	r := row(t, rep, rulesetName)
	if r.Status != StatusManual || !strings.Contains(r.Detail, "gh api repos/o/r/rulesets") || rep.Green() {
		t.Errorf("row = %+v", r)
	}
	Print(&out, rep, false)
	if !strings.Contains(out.String(), "manual: ruleset: ") {
		t.Errorf("out = %q", out.String())
	}
}

func TestAppPerms(t *testing.T) {
	for _, p := range []string{"workflows", "administration"} {
		f := policyFake()
		f.SetApp(appSlug, github.AppInfo{ID: 77, Permissions: map[string]string{p: "write"}})
		r := row(t, runCheck(t, f), appPermName)
		if r.Status != StatusDrift || !strings.Contains(r.Detail, p) || !strings.Contains(r.Detail, "settings/installations") {
			t.Errorf("%s: row = %+v", p, r)
		}
	}
}

func TestAppUnknownIsManual(t *testing.T) {
	f := fake.New()
	f.SetRulesets(goodRuleset())
	rep := runCheck(t, f)
	for _, n := range []string{rulesetName, appPermName} {
		if r := row(t, rep, n); r.Status != StatusManual || !strings.Contains(r.Detail, "gh api apps/"+appSlug) {
			t.Errorf("%s = %+v", n, r)
		}
	}
}

func TestMergeRebaseVersioning(t *testing.T) {
	for _, c := range []struct {
		val    string
		set    bool
		rebase bool
		want   Status
	}{
		{"on", true, true, StatusDrift},
		{"true", true, false, StatusOK},
		{"off", true, true, StatusOK},
		{"", false, true, StatusOK},
	} {
		f := policyFake()
		f.SetMergeSettings(github.MergeSettings{AllowRebaseMerge: c.rebase})
		if c.set {
			f.SetVariable("ARD_VERSIONING", c.val)
		}
		r := row(t, runCheck(t, f), mergeName)
		if r.Status != c.want {
			t.Errorf("%+v: row = %+v", c, r)
		}
		if c.want == StatusDrift && !strings.Contains(r.Detail, "gh api -X PATCH repos/o/r -F allow_rebase_merge=false") {
			t.Errorf("detail = %q", r.Detail)
		}
	}
}

func TestMergeVariableForbiddenIsManual(t *testing.T) {
	f := policyFake()
	f.FailNext("GetVariable", github.ErrForbidden)
	if r := row(t, runCheck(t, f), mergeName); r.Status != StatusManual || !strings.Contains(r.Detail, "gh variable get") {
		t.Errorf("row = %+v", r)
	}
}

func TestChecksNeverWrite(t *testing.T) {
	f := policyFake()
	f.SetMergeSettings(github.MergeSettings{AllowRebaseMerge: true})
	f.SetVariable("ARD_VERSIONING", "on")
	runCheck(t, f)
	if n := len(f.Writes()); n != 0 {
		t.Errorf("writes = %d", n)
	}
}

func TestPolicyDriftBlocksApply(t *testing.T) {
	f := policyFake()
	f.SetRulesets()
	rep, err := Run(context.Background(), policyDeps(f), Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); r.Status != StatusDrift || len(f.Writes()) != 0 {
		t.Errorf("labels = %+v writes = %d", r, len(f.Writes()))
	}
}

func TestPolicyGreenStillApplies(t *testing.T) {
	f := policyFake()
	rep, err := Run(context.Background(), policyDeps(f), Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); r.Status != StatusChanged {
		t.Errorf("labels = %+v", r)
	}
}
