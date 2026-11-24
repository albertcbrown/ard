package reviewfix

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	self   = "ard[bot]"
	engRev = "eng-review"
	secRev = "sec-review"
	wrk    = "engineer"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// stubGit answers from fields: no repository is involved.
type stubGit struct {
	tip      string   // RevParse answer; "" means the PR head
	changed  []string // CodeChanged answer
	head     string
	ancestor bool
	parents  []string
}

func (g *stubGit) FetchRef(context.Context, string, string, string, gitx.TokenFunc) error { return nil }
func (g *stubGit) RevParse(context.Context, string, string) (string, error) {
	if g.tip != "" {
		return g.tip, nil
	}
	return g.head, nil
}
func (g *stubGit) CodeChanged(context.Context, string, string, string, ...string) ([]string, error) {
	return g.changed, nil
}

func (g *stubGit) IsAncestor(context.Context, string, string, string) (bool, error) {
	return g.ancestor, nil
}
func (g *stubGit) Parents(context.Context, string, string) ([]string, error) {
	return g.parents, nil
}

type harness struct {
	t     *testing.T
	gh    *fake.Fake
	store *state.Store
	git   *stubGit
	loop  *Loop
	cfg   *config.Resolved
}

func newHarness(t *testing.T, max int, dry bool) *harness {
	t.Helper()
	return newHarnessAt(t, max, dry, nil)
}

// newHarnessAt opens the store with now as its clock when now is set.
func newHarnessAt(t *testing.T, max int, dry bool, now func() time.Time) *harness {
	t.Helper()
	h := &harness{t: t, git: &stubGit{head: "h2"},
		gh: fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}))}
	h.cfg = &config.Resolved{Repo: t.TempDir(), SHA: "abc", Roles: []config.Role{
		{Kind: config.KindReviewer, Name: engRev}, {Kind: config.KindReviewer, Name: secRev},
		{Kind: config.KindWorker, Name: wrk, MaxReviewRounds: max}}}
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st
	var fx effects.Effects = effects.NewLive(h.gh, gitx.New(""), quiet)
	if dry {
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: filepath.Join(t.TempDir(), "l.jsonl")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		fx = d
	}
	h.loop = New(Deps{Reader: h.gh, Effects: fx, Store: st, Git: h.git, RepoDir: t.TempDir(), RemoteURL: "x", Log: quiet})
	return h
}

// seeded is one issue with its worker PR.
type seeded struct{ issue, pr int }

func (h *harness) seed(issueLbl, prLbl []string, mut func(*github.PullRequest)) seeded {
	h.t.Helper()
	is := h.gh.SeedIssue(github.Issue{Title: "t", State: github.StateOpen, Author: "human", Labels: issueLbl})
	p := github.PullRequest{State: github.StateOpen, Author: self, Labels: prLbl, BaseRef: "main", HeadSHA: "h2",
		HeadRef: "worker/" + wrk + "-" + strconv.Itoa(is) + "-fix"}
	if mut != nil {
		mut(&p)
	}
	return seeded{issue: is, pr: h.gh.SeedPR(p)}
}

// closeoutChanges records a closed review of role asking for changes.
func (h *harness) closeoutChanges(s seeded, role string) {
	h.t.Helper()
	ctx := context.Background()
	j, err := h.store.CreateJob(ctx, state.NewJob{Stage: "review", Role: role, Issue: s.issue, PR: s.pr, Branch: "x"})
	if err != nil {
		h.t.Fatal(err)
	}
	rec := closeout.Record{Version: closeout.RecordVersion, Phase: closeout.PhaseClosed,
		Job:  closeout.Job{ID: j.ID, Stage: closeout.StageReview, Role: role, Issue: s.issue, PR: s.pr},
		Plan: closeout.Plan{Outcome: closeout.OutcomeChanges, Review: &closeout.ReviewPlan{CommitSHA: "h1", Verdict: "changes"}}}
	b, _ := json.Marshal(rec)
	if err := h.store.SetCloseout(ctx, j.ID, "closed", b); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Complete(ctx, j.ID, nil); err != nil {
		h.t.Fatal(err)
	}
}

// workerJob records a worker claim; done completes the job.
func (h *harness) workerJob(s seeded, done bool) {
	h.t.Helper()
	j, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: wrk, Issue: s.issue, Branch: "x"})
	if err != nil {
		h.t.Fatal(err)
	}
	if done {
		if err := h.store.Complete(context.Background(), j.ID, nil); err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) round(s seeded, n int, phase, head string) {
	h.t.Helper()
	err := h.loop.appendRound(context.Background(), s.issue, Round{N: n, Phase: phase, Reason: ReasonReview, PR: s.pr, Head: head, Worker: wrk, Roles: []string{engRev}})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) tick() error {
	h.t.Helper()
	return h.loop.Tick(context.Background(), controller.Env{Log: quiet, Cfg: h.cfg, Inflight: controller.NewInflight(context.Background(), quiet), Stopping: make(chan struct{})})
}

// phases lists the issue's round phases in order, joined by commas.
func (h *harness) phases(issue int) string {
	h.t.Helper()
	rs, err := Rounds(context.Background(), h.store, issue)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, r := range rs {
		out = append(out, r.Phase)
	}
	return strings.Join(out, ",")
}

func (h *harness) has(n int, label string) bool {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, l := range is.Labels {
		if strings.EqualFold(l, label) {
			return true
		}
	}
	return false
}

func (h *harness) comments(n int) int {
	h.t.Helper()
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return len(cs)
}

var (
	lblWorkerDone = labels.Done(labels.StageWorker, wrk)
	lblWorkerReq  = labels.Request(labels.StageWorker, wrk)
)

func reqDone(role string) []string {
	return []string{labels.Request(labels.StageReview, role), labels.Done(labels.StageReview, role)}
}
func reqChanges(role string) []string {
	return []string{labels.Request(labels.StageReview, role), labels.Changes(role)}
}

func stateJob(stage, role string, s seeded) state.NewJob {
	return state.NewJob{Stage: stage, Role: role, Issue: s.issue, PR: s.pr, Branch: "x"}
}
