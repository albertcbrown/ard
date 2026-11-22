package triage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

func TestLoopBasics(t *testing.T) {
	l := New(Deps{})
	if l.Name() != "triage" || !l.Claims() {
		t.Error("the triage loop claims, so it is skipped while paused")
	}
	if l.Interval(nil) != 15*time.Minute || l.TickTimeout(nil) != 5*time.Minute {
		t.Error("defaults")
	}
	cfg := &config.Resolved{}
	cfg.Intervals.Triage = config.Duration(20 * time.Minute)
	if l.Interval(cfg) != 20*time.Minute {
		t.Error("interval from config")
	}
}

// A lead claim on a human issue labelled by a maintainer.
func TestClaimLead(t *testing.T) {
	h := newHarness(t)
	n := h.human("Crash", "body text", lead)
	h.labelledBy(n, lead, "maint", "maintain", t0.Add(time.Hour))
	h.gh.SeedComment(n, github.Comment{Author: "bob", Body: "me too", CreatedAt: t0.Add(2 * time.Hour)})
	h.mustTick()

	if h.pool.offers != 1 || len(h.pool.last) != 1 {
		t.Fatalf("offers = %d, last = %v", h.pool.offers, h.pool.last)
	}
	if got := h.fin.got(); len(got) != 1 || got[0].Cause.Kind != "" || got[0].JobID == "" {
		t.Fatalf("finish calls = %+v", got)
	}
	if !h.has(n, labels.Running(labels.StageTriage, "lead")) {
		t.Error("no :running label")
	}
	if len(h.ex.prepared) != 1 || len(h.ex.started) != 1 {
		t.Fatalf("prepared %d started %d", len(h.ex.prepared), len(h.ex.started))
	}
	j := h.ex.prepared[0]
	if j.Stage != "triage" || j.Role != "lead" || j.Issue != n || j.Ref != "abc123" || j.Repo != "o/r" || j.Timeout != 30*time.Minute {
		t.Errorf("job = %+v", j)
	}
	wantScrub := fmt.Sprintf(".ard/%d/triage-lead.md", n)
	if len(j.Scrub) != 1 || j.Scrub[0] != wantScrub || j.Scrub[0] != agent.RoleFilePath(n, "triage", "lead") {
		t.Errorf("scrub = %v", j.Scrub)
	}
	var paths []string
	for _, f := range j.Inputs {
		paths = append(paths, f.Path)
		if strings.HasPrefix(f.Path, ".ard/") {
			t.Errorf("untrusted input under .ard/: %s", f.Path)
		}
		if !strings.HasPrefix(string(f.Data), untrustedHeader) {
			t.Errorf("%s lacks the untrusted header", f.Path)
		}
	}
	dir := fmt.Sprintf(".ard-input/%d/", n)
	if strings.Join(paths, ",") != dir+"issue.md,"+dir+"comments.md" {
		t.Errorf("inputs = %v", paths)
	}
	args := strings.Join(h.ex.started[0].Args, " ")
	if !strings.Contains(args, dir+"issue.md") || !strings.Contains(args, dir+"comments.md") {
		t.Errorf("the agent is not told about the untrusted files:\n%s", args)
	}
	a, err := h.store.Admission(context.Background(), n)
	if err != nil || a.LabeledBy != "maint" || a.BodyHash != BodyHash(h.issue(n)) || a.JobID != h.fin.got()[0].JobID || a.LabeledAt.IsZero() {
		t.Errorf("admission = %+v, %v", a, err)
	}
	if h.pool.Pool != nil {
		if _, held := h.pool.Lookup(pool.KeyFor(pool.ClassTriage, "lead", n)); held {
			t.Error("the slot is still held after Finish")
		}
	}
	// A second tick does not claim again while the job row is running.
	h.mustTick()
	if len(h.ex.prepared) != 1 {
		t.Error("claimed twice")
	}
}

func TestAppAuthoredIssueIsExemptFromTheHash(t *testing.T) {
	h := newHarness(t)
	n := h.gh.SeedIssue(github.Issue{Title: "scan", Body: "v1", Author: self, Labels: []string{lead}, CreatedAt: t0})
	h.mustTick()
	if len(h.ex.prepared) != 1 {
		t.Fatal("scanner issue not claimed")
	}
	if _, err := h.store.Admission(context.Background(), n); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("an App-authored issue gets no admission record: %v", err)
	}
}

type staleReader struct {
	Reader
	stale map[int]github.Issue
}

func (s staleReader) SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error) {
	out, err := s.Reader.SearchIssues(ctx, q, o)
	for _, is := range s.stale {
		out = append(out, is)
	}
	return out, err
}

func TestCandidates(t *testing.T) {
	t.Run("stale search hit is dropped after the re-read", func(t *testing.T) {
		h := newHarness(t)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead, labels.Done(labels.StageTriage, "lead")}, CreatedAt: t0})
		stale := h.issue(n)
		stale.Labels = []string{lead} // what the lagging search index still says
		h.loop.d.Reader = staleReader{h.loop.d.Reader, map[int]github.Issue{n: stale}}
		h.mustTick()
		if len(h.ex.prepared) != 0 || h.pool.offers != 1 || len(h.pool.last) != 0 {
			t.Errorf("prepared %d, offers %d, last %v", len(h.ex.prepared), h.pool.offers, h.pool.last)
		}
	})
	t.Run("two roles on one issue claim one, the lead first", func(t *testing.T) {
		h := newHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead, labels.Request(labels.StageTriage, "architect")}, CreatedAt: t0})
		h.mustTick()
		if len(h.ex.prepared) != 1 || h.ex.prepared[0].Role != "lead" {
			t.Fatalf("prepared = %+v", h.ex.prepared)
		}
	})
	t.Run("a running triage job blocks another role", func(t *testing.T) {
		h := newHarness(t)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{labels.Request(labels.StageTriage, "architect")}, CreatedAt: t0})
		if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "triage", Role: "lead", Issue: n}); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if len(h.ex.prepared) != 0 {
			t.Error("a second triage job started on the issue")
		}
	})
	t.Run("excluded labels", func(t *testing.T) {
		for _, ex := range []string{labels.NeedsHuman, labels.Hold, labels.ExecutorFailed, labels.Running(labels.StageTriage, "lead")} {
			h := newHarness(t)
			h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead, ex}, CreatedAt: t0})
			h.mustTick()
			if len(h.ex.prepared) != 0 {
				t.Errorf("%s: claimed", ex)
			}
		}
	})
	t.Run("a closed issue and a PR are never claimed", func(t *testing.T) {
		h := newHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, State: github.StateClosed, CreatedAt: t0})
		h.gh.SeedPR(github.PullRequest{Title: "p", Author: self, Labels: []string{lead}, State: github.StateOpen})
		h.mustTick()
		if len(h.ex.prepared) != 0 {
			t.Error("claimed")
		}
	})
	t.Run("a non-lead request on a human issue with no admission is skipped", func(t *testing.T) {
		h := newHarness(t)
		h.human("t", "b", labels.Request(labels.StageTriage, "architect"))
		h.mustTick()
		if len(h.ex.prepared) != 0 {
			t.Error("claimed")
		}
	})
	t.Run("no lead role offers nothing and claims nothing", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.Roles = h.cfg.Roles[:1] // architect only
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		h.mustTick()
		if len(h.ex.prepared) != 0 || h.pool.offers != 1 {
			t.Errorf("prepared %d offers %d", len(h.ex.prepared), h.pool.offers)
		}
	})
	t.Run("no config", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.SHA = ""
		h.mustTick()
		if h.pool.offers != 1 {
			t.Errorf("offers = %d", h.pool.offers)
		}
	})
}

func TestNoModelCredential(t *testing.T) {
	h := newHarness(t, noCred)
	n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
	h.mustTick()
	h.mustTick()
	if len(h.ex.prepared) != 0 || len(h.gh.Writes()) != 0 || len(h.jobs()) != 0 || h.pool.offers != 2 || len(h.pool.last) != 0 {
		t.Errorf("a claim happened: prepared %d writes %v jobs %v offers %d", len(h.ex.prepared), h.gh.Writes(), h.jobs(), h.pool.offers)
	}
	if h.has(n, labels.Running(labels.StageTriage, "lead")) {
		t.Error("labelled")
	}
}

type conflictStore struct{ Store }

func (conflictStore) CreateJob(context.Context, state.NewJob) (state.Job, error) {
	return state.Job{}, fmt.Errorf("claim: %w", state.ErrConflict)
}

func TestClaimFailures(t *testing.T) {
	t.Run("a conflict writes nothing", func(t *testing.T) {
		h := newHarness(t)
		n := h.human("t", "b", lead)
		h.labelledBy(n, lead, "maint", "write", t0.Add(time.Hour))
		h.loop.d.Store = conflictStore{h.loop.d.Store}
		h.mustTick()
		if len(h.gh.Writes()) != 0 || len(h.ex.prepared) != 0 || len(h.fin.got()) != 0 {
			t.Errorf("writes %v prepared %d", h.gh.Writes(), len(h.ex.prepared))
		}
		if _, err := h.store.Admission(context.Background(), n); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("admission recorded: %v", err)
		}
		if _, held := h.pool.Lookup(pool.KeyFor(pool.ClassTriage, "lead", n)); held {
			t.Error("slot leaked")
		}
	})
	t.Run("the running label fails: the job fails in state, the slot is released", func(t *testing.T) {
		h := newHarness(t)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		h.gh.FailNext("AddLabels", errors.New("boom"))
		h.mustTick()
		if len(h.fin.got()) != 0 || len(h.ex.prepared) != 0 {
			t.Error("a job ran")
		}
		var removed bool
		for _, c := range h.gh.Writes() {
			removed = removed || (c.Method == "RemoveLabel" && c.Args[1] == labels.Running(labels.StageTriage, "lead"))
		}
		if !removed {
			t.Errorf("no best-effort unlabel: %v", h.gh.Writes())
		}
		if len(h.jobs()) != 0 {
			t.Error("the job is still running in state")
		}
		if _, held := h.pool.Lookup(pool.KeyFor(pool.ClassTriage, "lead", n)); held {
			t.Error("slot leaked")
		}
		// The next poll claims again, as a new job.
		h.mustTick()
		if len(h.ex.prepared) != 1 {
			t.Error("not claimed again")
		}
	})
	t.Run("clone failure is a transient clone_failed cause", func(t *testing.T) {
		h := newHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		h.ex.prepareErr = fmt.Errorf("add worktree: %w: /home/secret/path", executor.ErrClone)
		h.mustTick()
		got := h.fin.got()
		if len(got) != 1 || got[0].Cause.Kind != closeout.CauseCloneFail || !got[0].Cause.Transient || !got[0].Cause.Retry {
			t.Fatalf("finish = %+v", got)
		}
		if strings.Contains(got[0].Cause.Detail, "secret") {
			t.Error("the cause detail leaks the error text")
		}
	})
	t.Run("another prepare error is no_process", func(t *testing.T) {
		h := newHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		h.ex.prepareErr = errors.New("disk full")
		h.mustTick()
		got := h.fin.got()
		if len(got) != 1 || got[0].Cause.Kind != closeout.CauseNoProcess || got[0].Cause.Transient {
			t.Fatalf("finish = %+v", got)
		}
	})
	t.Run("a start error is no_process", func(t *testing.T) {
		h := newHarness(t)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		h.ex.startErr = errors.New("exec failed")
		h.mustTick()
		got := h.fin.got()
		if len(got) != 1 || got[0].Cause.Kind != closeout.CauseNoProcess || got[0].H.JobID == "" {
			t.Fatalf("finish = %+v", got)
		}
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatcher(t *testing.T) {
	t.Run("shutdown returns without Finish and leaves the job running", func(t *testing.T) {
		h := newHarness(t)
		h.ex.running = make(chan struct{})
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		in, err := h.tickAsync()
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "start", func() bool { return h.ex.nStarted() == 1 })
		close(h.stop)
		select {
		case <-in.Wait():
		case <-time.After(5 * time.Second):
			t.Fatal("watcher did not return")
		}
		if len(h.fin.got()) != 0 {
			t.Error("Finish was called on shutdown")
		}
		if js := h.jobs(); len(js) != 1 {
			t.Errorf("running jobs = %v", js)
		}
		if _, held := h.pool.Lookup(pool.KeyFor(pool.ClassTriage, "lead", n)); held {
			t.Error("slot not released")
		}
	})
	t.Run("the slot is held until Finish returns", func(t *testing.T) {
		h := newHarness(t)
		h.fin.block = make(chan struct{})
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		in, err := h.tickAsync()
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "start", func() bool { return h.ex.nStarted() == 1 })
		time.Sleep(50 * time.Millisecond) // the watcher is now inside the blocked Finish
		cand := pool.Candidate{Class: pool.ClassTriage, Role: "lead", Number: n, Since: t0}
		if d := h.pool.Offer("other", time.Minute, []pool.Candidate{cand}); d[0].Verdict == pool.Granted {
			t.Fatal("the slot was granted again while Finish was running")
		}
		close(h.fin.block)
		<-in.Wait()
		d := h.pool.Offer("other", time.Minute, []pool.Candidate{cand})
		if d[0].Verdict != pool.Granted {
			t.Errorf("after Finish: %v", d[0].Verdict)
		}
	})
	t.Run("a Reaper kill is seen as Gone and closed out as heartbeat_quiet", func(t *testing.T) {
		h := newHarness(t)
		h.ex.running = make(chan struct{})
		var ran []closeout.Job
		h.loop.d.Finisher = &reaper.Finisher{Closer: recCloser{&ran}, Cleaner: h.ex, Store: h.store, Start: time.Now().Add(-time.Hour), Log: quiet}
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
		in, err := h.tickAsync()
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "start", func() bool { return h.ex.nStarted() == 1 })
		js := h.jobs()
		if len(js) != 1 {
			t.Fatalf("jobs = %v", js)
		}
		// What the Reaper does to an owned quiet job: record why, then kill.
		if err := h.store.SetKillReason(context.Background(), js[0].ID, string(closeout.CauseQuiet)); err != nil {
			t.Fatal(err)
		}
		h.ex.setGone()
		<-in.Wait()
		if len(ran) != 1 || ran[0].Cause.Kind != closeout.CauseQuiet || ran[0].Cause.Retry {
			t.Fatalf("close-out = %+v", ran)
		}
	})
}

type recCloser struct{ ran *[]closeout.Job }

func (r recCloser) Run(_ context.Context, j closeout.Job) (closeout.Outcome, error) {
	*r.ran = append(*r.ran, j)
	return closeout.OutcomeFailed, nil
}

func (r recCloser) ResumeJob(context.Context, string) (closeout.Outcome, error) {
	return "", errors.New("unexpected resume")
}

// In dry-run the loop logs would_claim and stops: nothing is written, no job
// row exists and no slot is taken, so the Reaper's next tick finds nothing to do.
func TestDryRunWouldClaimAndStop(t *testing.T) {
	h := newHarness(t, dryRun)
	n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{lead}, CreatedAt: t0})
	h.mustTick()
	h.mustTick()
	if h.ex.nPrepared() != 0 || len(h.fin.got()) != 0 || len(h.gh.Writes()) != 0 || len(h.jobs()) != 0 {
		t.Fatalf("dry-run claimed: prepared %d writes %v jobs %v", h.ex.nPrepared(), h.gh.Writes(), h.jobs())
	}
	if h.pool.offers != 2 || len(h.pool.last) != 0 {
		t.Errorf("offers %d, last %v: dry-run must hold no slot", h.pool.offers, h.pool.last)
	}
	if _, held := h.pool.Lookup(pool.KeyFor(pool.ClassTriage, "lead", n)); held {
		t.Error("slot held")
	}
	if got := readLedger(t, h.ledger); got != "" {
		t.Errorf("ledger written by the triage tick: %s", got)
	}
	if h.has(n, labels.Running(labels.StageTriage, "lead")) {
		t.Error("labelled")
	}

	// The Reaper's next tick has no effects either: no recordless :running
	// label, nothing running, no close-out to resume.
	r := reaper.New(reaper.Deps{
		Store: h.store, Executor: h.ex, Finisher: &reaper.Finisher{Closer: recCloser{new([]closeout.Job)}, Cleaner: h.ex, Store: h.store, Log: quiet},
		Closer: recCloser{new([]closeout.Job)}, Reader: h.gh, Effects: h.fx, Log: quiet,
		Parse: func(context.Context, *config.Resolved, state.Job, executor.Handle, int) (agent.Result, error) {
			return agent.Result{}, nil
		},
	})
	for i := 0; i < 10; i++ {
		if err := r.Tick(context.Background(), controller.Env{Cfg: h.cfg, Log: quiet}); err != nil {
			t.Fatal(err)
		}
	}
	if got := readLedger(t, h.ledger); got != "" || len(h.gh.Writes()) != 0 {
		t.Errorf("the Reaper acted after a dry-run tick: ledger %q writes %v", got, h.gh.Writes())
	}
}

func TestDryRunIntakeWritesNothing(t *testing.T) {
	h := newHarness(t, dryRun, allMode)
	h.gh.SeedIssue(github.Issue{Title: "status", Author: self, CreatedAt: t0})
	h.mustTick() // sets EnabledAt
	n := h.gh.SeedIssue(github.Issue{Title: "new", Author: "alice", CreatedAt: t0.Add(time.Hour)})
	h.mustTick()
	if h.has(n, lead) || len(h.gh.Writes()) != 0 || readLedger(t, h.ledger) != "" || h.ex.nPrepared() != 0 {
		t.Errorf("dry-run intake wrote: writes %v ledger %q", h.gh.Writes(), readLedger(t, h.ledger))
	}
}
