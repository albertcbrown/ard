package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (h *harness) readyTick() (string, error) {
	h.t.Helper()
	buf := &syncBuf{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	in := controller.NewInflight(context.Background(), quiet)
	err := h.loop.Tick(context.Background(), controller.Env{Log: log, Cfg: h.cfg, Inflight: in, Stopping: h.stop})
	<-in.Wait()
	return buf.String(), err
}

func (h *harness) claim(issue int, role, head string) {
	h.t.Helper()
	data, _ := json.Marshal(map[string]string{"head": head})
	if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventClaim, Issue: issue, Stage: stageReview, Role: role, Data: data}); err != nil {
		h.t.Fatal(err)
	}
}

func doneLabels(roles ...string) []string {
	var out []string
	for _, r := range roles {
		out = append(out, labels.Request(labels.StageReview, r), labels.Done(labels.StageReview, r))
	}
	return out
}

func (h *harness) draft(n int) bool {
	h.t.Helper()
	pr, err := h.gh.GetPR(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return pr.Draft
}

func (h *harness) marks() int {
	n := 0
	for _, c := range h.gh.Writes() {
		if c.Method == "MarkReady" {
			n++
		}
	}
	return n
}

func readyScenario(h *harness, roles []string, mut ...func(*github.PullRequest)) (int, int) {
	return h.scenario(func(s *scenario) {
		s.pr.Labels = doneLabels(roles...)
		for _, m := range mut {
			m(&s.pr)
		}
	})
}

func TestMarksReadyWhenLastReviewDone(t *testing.T) {
	h := newHarness(t)
	pr, issue := readyScenario(h, []string{reviewer, "sec-review"})
	h.claim(issue, reviewer, tipSHA)
	h.claim(issue, "sec-review", tipSHA)
	out, err := h.readyTick()
	if err != nil {
		t.Fatal(err)
	}
	if h.draft(pr) || h.marks() != 1 {
		t.Fatalf("draft=%v marks=%d", h.draft(pr), h.marks())
	}
	if !strings.Contains(out, "review.marked_ready") || !strings.Contains(out, "head="+tipSHA) {
		t.Errorf("log: %s", out)
	}
	// Never marks twice.
	h.mustTick()
	if h.marks() != 1 {
		t.Errorf("marks = %d", h.marks())
	}
}

func TestNoMarkWhileBlocked(t *testing.T) {
	cases := map[string]func(*github.PullRequest){
		"changes": func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.Changes(reviewer)) },
		"running": func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.Running(labels.StageReview, reviewer)) },
		"unfinished": func(p *github.PullRequest) {
			p.Labels = append(p.Labels, labels.Request(labels.StageReview, "sec-review"))
		},
		"executor":     func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.ExecutorFailed) },
		"needs-human":  func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.NeedsHuman) },
		"hold":         func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.Hold) },
		"tests-failed": func(p *github.PullRequest) { p.Labels = append(p.Labels, labels.TestsFailed) },
		"no-request":   func(p *github.PullRequest) { p.Labels = nil },
		"closed":       func(p *github.PullRequest) { p.State = github.StateClosed },
		"human":        func(p *github.PullRequest) { p.Author = "someone" },
		"base":         func(p *github.PullRequest) { p.BaseRef = "dev" },
		"branch":       func(p *github.PullRequest) { p.HeadRef = "feature/x" },
		"ready":        func(p *github.PullRequest) { p.Draft = false },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			pr, issue := readyScenario(h, []string{reviewer}, mut)
			h.claim(issue, reviewer, tipSHA)
			h.claim(issue, "sec-review", tipSHA)
			h.mustTick()
			if h.marks() != 0 {
				t.Fatalf("marked PR %d", pr)
			}
		})
	}
}

func TestNoMarkWhenHeadMoved(t *testing.T) {
	const old = "3333333333333333333333333333333333333333"
	for name, tc := range map[string]struct {
		changed []string
		err     error
		claim   string
		want    int
	}{
		"code changed": {changed: []string{"a.go"}, claim: old, want: 0},
		"ard only":     {claim: old, want: 1},
		"git error":    {err: errBoom, claim: old, want: 0},
		"no claim":     {claim: "", want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.git.changed, h.git.changedErr = tc.changed, tc.err
			_, issue := readyScenario(h, []string{reviewer})
			if tc.claim != "" {
				h.claim(issue, reviewer, tc.claim)
			}
			h.mustTick()
			if h.marks() != tc.want {
				t.Fatalf("marks = %d, want %d", h.marks(), tc.want)
			}
		})
	}
}

func TestDryRunOnlyLedger(t *testing.T) {
	h := newHarness(t, dryRun)
	pr, issue := readyScenario(h, []string{reviewer})
	h.claim(issue, reviewer, tipSHA)
	h.mustTick()
	if !h.draft(pr) || h.marks() != 0 {
		t.Fatal("dry run changed the PR")
	}
	if l := readLedger(t, h.ledger); strings.Count(l, "MarkReady") != 1 {
		t.Errorf("ledger: %q", l)
	}
}

func TestSweepWorksWithoutCredential(t *testing.T) {
	h := newHarness(t, noCred)
	pr, issue := readyScenario(h, []string{reviewer})
	h.claim(issue, reviewer, tipSHA)
	h.mustTick()
	if h.draft(pr) {
		t.Fatal("missed mark was not caught")
	}
}

func TestMarkReadyFailureIsRetried(t *testing.T) {
	h := newHarness(t)
	pr, issue := readyScenario(h, []string{reviewer})
	h.claim(issue, reviewer, tipSHA)
	h.gh.FailNext("MarkReady", errBoom)
	out, err := h.readyTick()
	if err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	if !strings.Contains(out, "review.mark_ready_failed") || !h.draft(pr) {
		t.Fatalf("log: %s", out)
	}
	h.mustTick()
	if h.draft(pr) {
		t.Fatal("not retried")
	}
}

// finishDone scripts the fake close-out to end the review done: the running
// label goes, the done label comes.
func finishDone(h *harness, pr int) {
	h.fin.after = func() {
		ctx := context.Background()
		_ = h.gh.RemoveLabel(ctx, pr, labels.Running(labels.StageReview, reviewer))
		_ = h.gh.AddLabels(ctx, pr, labels.Done(labels.StageReview, reviewer))
	}
}

func TestPostCloseoutMarksReady(t *testing.T) {
	h := newHarness(t)
	pr, _ := h.scenario()
	finishDone(h, pr)
	h.mustTick()
	if len(h.fin.got()) != 1 {
		t.Fatalf("finish calls = %d", len(h.fin.got()))
	}
	// The run itself marked it: no sweep has had a chance to.
	if h.draft(pr) || h.marks() != 1 {
		t.Fatalf("draft=%v marks=%d", h.draft(pr), h.marks())
	}
}

func TestMarkReadyFailureDoesNotFailCloseout(t *testing.T) {
	h := newHarness(t)
	pr, _ := h.scenario()
	finishDone(h, pr)
	h.gh.FailNext("MarkReady", errBoom)
	out, err := h.readyTick()
	if err != nil {
		t.Fatalf("tick failed: %v", err)
	}
	calls := h.fin.got()
	if len(calls) != 1 || calls[0].Opts.Cause.Kind != "" {
		t.Fatalf("close-out changed: %+v", calls)
	}
	if !strings.Contains(out, "review.mark_ready_failed") || !h.draft(pr) {
		t.Fatalf("draft=%v log: %s", h.draft(pr), out)
	}
	// The next tick's sweep retries and succeeds once.
	h.mustTick()
	if h.draft(pr) {
		t.Fatal("not retried")
	}
	if len(h.fin.got()) != 1 {
		t.Errorf("review re-ran: %d finish calls", len(h.fin.got()))
	}
}
