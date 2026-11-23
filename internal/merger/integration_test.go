//go:build integration

package merger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	gogh "github.com/google/go-github/v88/github"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/merger"
	"github.com/albertcbrown/ard/internal/state"
)

// TestIntegration runs the merger against a real scratch repository. It skips
// unless ARD_IT_REPO (owner/name) and the ARD_GITHUB_* App credentials are
// set. It creates issues, branches and PRs in that repo, merges some of them
// into main and pushes commits to main: point it at a scratch repository.
//
// The default target is ard-scratch exactly as it is: private, free plan, no
// ruleset and no CI. The rules API then answers 403 ("Upgrade to GitHub Pro"),
// so the merger runs its no-rules, no-CI path: reviews alone, after the grace
// period (about two minutes per merge, so a full run takes a while). That
// covers the merge (case 1), update-branch (case 2), the protected-path
// approval (case 3) and the conflict path (case 4).
//
// Optional:
//
//	ARD_IT_APPROVER_TOKEN  a token of a write collaborator who is not the App;
//	                       without it case 3 stops after the needs-approval half.
//	ARD_IT_CI=1            the repo has a workflow job (for example "ci") that
//	                       fails when a file named ci-fail exists, and a ruleset
//	                       on main that requires it (App on the bypass list).
//	                       Enables case 5, the red required check. Run it on a
//	                       repository that has rulesets (public, or a paid plan).
//
// One-time scratch-repo setup for case 5: add .github/workflows/ci.yml with a
// job "ci" that exits 1 when ci-fail exists, add a ruleset on main requiring
// that check with the App on the bypass list (Always), run `ard labels sync`.
func TestIntegration(t *testing.T) {
	repoName := os.Getenv("ARD_IT_REPO")
	if repoName == "" {
		t.Skip("ARD_IT_REPO not set")
	}
	repo, err := github.ParseRepo(repoName)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		t.Skipf("no credentials: %v", err)
	}
	c, err := github.New(github.Config{Repo: repo, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Kind != github.AuthApp {
		t.Skip("the merger test needs App credentials: the PRs must be authored by the App")
	}
	rt, err := auth.Transport(http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}
	gh, err := gogh.NewClient(gogh.WithHTTPClient(&http.Client{Transport: rt, Timeout: time.Minute}), gogh.WithUserAgent("ard-it"))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, repo: repo, c: c, gh: gh, auth: auth, self: id.Login}
	e.base = "main"
	t.Run("merge", e.caseMerge)
	t.Run("behind", e.caseBehind)
	t.Run("protected", e.caseProtected)
	t.Run("conflict", e.caseConflict)
	t.Run("red check", e.caseRedCheck)
}

type env struct {
	t    *testing.T
	repo github.Repo
	c    github.Client
	gh   *gogh.Client
	auth github.Auth
	self string
	base string
}

// scenario is one PR the test created.
type scenario struct {
	e      *env
	issue  int
	pr     int
	branch string
	head   string
	store  *state.Store
	loop   *merger.Loop
	jobID  string
}

func (e *env) uniq() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

func (e *env) tip(branch string) string {
	e.t.Helper()
	ref, _, err := e.gh.Git.GetRef(context.Background(), e.repo.Owner, e.repo.Name, "heads/"+branch)
	if err != nil {
		e.t.Fatalf("get ref %s: %v", branch, err)
	}
	return ref.Object.GetSHA()
}

// commit adds files (path -> body) on top of parent and returns the new commit.
func (e *env) commit(parent string, files map[string]string, msg string) string {
	e.t.Helper()
	ctx := context.Background()
	pc, _, err := e.gh.Git.GetCommit(ctx, e.repo.Owner, e.repo.Name, parent)
	if err != nil {
		e.t.Fatalf("get commit: %v", err)
	}
	var entries []*gogh.TreeEntry
	for p, body := range files {
		entries = append(entries, &gogh.TreeEntry{Path: gogh.Ptr(p), Mode: gogh.Ptr("100644"), Type: gogh.Ptr("blob"), Content: gogh.Ptr(body)})
	}
	tree, _, err := e.gh.Git.CreateTree(ctx, e.repo.Owner, e.repo.Name, pc.Tree.GetSHA(), entries)
	if err != nil {
		e.t.Fatalf("create tree: %v", err)
	}
	cm, _, err := e.gh.Git.CreateCommit(ctx, e.repo.Owner, e.repo.Name, gogh.Commit{Message: gogh.Ptr(msg), Tree: tree, Parents: []*gogh.Commit{{SHA: gogh.Ptr(parent)}}}, nil)
	if err != nil {
		e.t.Fatalf("create commit: %v", err)
	}
	return cm.GetSHA()
}

func (e *env) pushMain(files map[string]string, msg string) string {
	e.t.Helper()
	sha := e.commit(e.tip(e.base), files, msg)
	if _, _, err := e.gh.Git.UpdateRef(context.Background(), e.repo.Owner, e.repo.Name, "heads/"+e.base, gogh.UpdateRef{SHA: sha}); err != nil {
		e.t.Fatalf("update main: %v", err)
	}
	return sha
}

// newScenario creates an issue, a branch worker/engineer-<issue>-it-<uniq>
// holding files (cut from from, default main's tip), a PR authored by the App
// whose body starts "Closes #<issue>", the review labels, and the controller
// state a finished worker and reviewer leave behind.
func (e *env) newScenario(files map[string]string, from string) *scenario {
	e.t.Helper()
	ctx := context.Background()
	u := e.uniq()
	is, err := e.c.CreateIssue(ctx, github.NewIssue{Title: "merger integration " + u, Body: "Created by the merger integration test; safe to close."})
	if err != nil {
		e.t.Fatal(err)
	}
	s := &scenario{e: e, issue: is.Number, branch: fmt.Sprintf("worker/engineer-%d-it-%s", is.Number, u)}
	if from == "" {
		from = e.tip(e.base)
	}
	files[fmt.Sprintf(".ard/%d/worker-engineer.md", is.Number)] = "integration\n"
	s.head = e.commit(from, files, "merger integration "+u)
	if _, _, err := e.gh.Git.CreateRef(ctx, e.repo.Owner, e.repo.Name, gogh.CreateRef{Ref: "refs/heads/" + s.branch, SHA: s.head}); err != nil {
		e.t.Fatal(err)
	}
	pr, err := e.c.CreatePR(ctx, github.NewPR{Title: "merger integration " + u, Body: fmt.Sprintf("Closes #%d\n\nIntegration test.", is.Number), Head: s.branch, Base: e.base})
	if err != nil {
		e.t.Fatal(err)
	}
	s.pr = pr.Number
	e.t.Cleanup(func() {
		bg := context.Background()
		_, _, _ = e.gh.PullRequests.Edit(bg, e.repo.Owner, e.repo.Name, s.pr, &gogh.PullRequest{State: gogh.Ptr("closed")})
		_, _ = e.gh.Git.DeleteRef(bg, e.repo.Owner, e.repo.Name, "heads/"+s.branch)
		closed := github.StateClosed
		_ = e.c.UpdateIssue(bg, s.issue, github.IssueUpdate{State: &closed})
	})
	if err := e.c.AddLabels(ctx, s.pr, labels.Request(labels.StageReview, "eng-review"), labels.Done(labels.StageReview, "eng-review")); err != nil {
		e.t.Fatal(err)
	}
	st, err := state.Open(ctx, e.t.TempDir(), state.Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { st.Close() })
	s.store = st
	rj, err := st.CreateJob(ctx, state.NewJob{Stage: "review", Role: "eng-review", Issue: s.issue, PR: s.pr, Branch: s.branch})
	if err != nil {
		e.t.Fatal(err)
	}
	rec, _ := json.Marshal(closeout.Record{Version: closeout.RecordVersion, Phase: closeout.PhaseClosed,
		Job:  closeout.Job{ID: rj.ID, Stage: closeout.StageReview, Role: "eng-review", Issue: s.issue, PR: s.pr},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Review: &closeout.ReviewPlan{CommitSHA: s.head, Verdict: "approve"}}})
	if err := st.SetCloseout(ctx, rj.ID, "closed", rec); err != nil {
		e.t.Fatal(err)
	}
	if err := st.Complete(ctx, rj.ID, nil); err != nil {
		e.t.Fatal(err)
	}
	j, err := st.CreateJob(ctx, state.NewJob{Stage: "worker", Role: "engineer", Issue: s.issue, Branch: s.branch})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := st.SetCloseout(ctx, j.ID, "closed", json.RawMessage(`{}`)); err != nil {
		e.t.Fatal(err)
	}
	if err := st.Complete(ctx, j.ID, nil); err != nil {
		e.t.Fatal(err)
	}
	s.jobID = j.ID

	clone := e.t.TempDir()
	g := gitx.New("")
	if err := g.InitBare(ctx, clone); err != nil {
		e.t.Fatal(err)
	}
	s.loop = merger.New(merger.Deps{
		Reader: e.c, Effects: effects.NewLive(e.c, g, slog.New(slog.NewTextHandler(io.Discard, nil))), Store: st, Git: g,
		RepoDir: clone, RemoteURL: fmt.Sprintf("https://github.com/%s.git", e.repo), Token: e.auth.Token,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	})
	return s
}

// tickUntil ticks the loop every 15 seconds until done reports true or the
// deadline passes.
func (s *scenario) tickUntil(what string, deadline time.Duration, done func() bool) {
	s.e.t.Helper()
	cfg := &config.Resolved{Repo: s.e.t.TempDir(), SHA: "it", ProtectedPaths: []string{".claude/"},
		Roles: []config.Role{{Kind: config.KindReviewer, Name: "eng-review"}}}
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		err := s.loop.Tick(context.Background(), controller.Env{Cfg: cfg, Inflight: controller.NewInflight(context.Background(), slog.Default())})
		if err != nil {
			s.e.t.Logf("tick: %v", err)
		}
		if done() {
			return
		}
		time.Sleep(15 * time.Second)
	}
	s.e.t.Fatalf("timed out waiting for %s", what)
}

func (s *scenario) pull() github.PullRequest {
	s.e.t.Helper()
	pr, err := s.e.c.GetPR(context.Background(), s.pr)
	if err != nil {
		s.e.t.Fatal(err)
	}
	return pr
}

func (s *scenario) merged() bool { return s.pull().Merged }

func (s *scenario) hasLabel(l string) bool {
	for _, x := range s.pull().Labels {
		if x == l {
			return true
		}
	}
	return false
}

// assertSquash checks the merged PR's squash commit.
func (s *scenario) assertSquash() {
	s.e.t.Helper()
	ctx := context.Background()
	evs, err := s.store.Events(ctx, state.EventFilter{Kind: state.EventMerge})
	if err != nil || len(evs) != 1 {
		s.e.t.Fatalf("merge events: %v %v", evs, err)
	}
	var data struct {
		MergeSHA string `json:"merge_sha"`
	}
	if err := json.Unmarshal(evs[0].Data, &data); err != nil || data.MergeSHA == "" {
		s.e.t.Fatalf("merge event data: %s", evs[0].Data)
	}
	cm, _, err := s.e.gh.Git.GetCommit(ctx, s.e.repo.Owner, s.e.repo.Name, data.MergeSHA)
	if err != nil {
		s.e.t.Fatal(err)
	}
	if !strings.Contains(cm.GetMessage(), fmt.Sprintf("(#%d)", s.pr)) || !strings.Contains(cm.GetMessage(), "Ard-Job: "+s.jobID) {
		s.e.t.Errorf("squash message %q", cm.GetMessage())
	}
	// Decision 11: a GitHub squash merge has GitHub as committer, so the
	// bot-commit audit (which keys on the committer) does not flag it. If this
	// fails, API merges record the App as committer and the audit needs a decision.
	if got := cm.GetCommitter().GetEmail(); got != "noreply@github.com" {
		s.e.t.Errorf("squash commit committer = %q, want noreply@github.com", got)
	}
	is, err := s.e.c.GetIssue(ctx, s.issue)
	if err != nil {
		s.e.t.Fatal(err)
	}
	if is.State != github.StateClosed {
		s.e.t.Error("issue not closed by the PR body's Closes line")
	}
	if _, _, err := s.e.gh.Git.GetRef(ctx, s.e.repo.Owner, s.e.repo.Name, "heads/"+s.branch); err == nil {
		s.e.t.Error("branch not deleted")
	}
}

func (e *env) caseMerge(t *testing.T) {
	s := e.newScenario(map[string]string{"it/" + e.uniq() + ".txt": "merge\n"}, "")
	s.tickUntil("the PR to merge", 12*time.Minute, s.merged)
	s.assertSquash()
}

func (e *env) caseBehind(t *testing.T) {
	s := e.newScenario(map[string]string{"it/" + e.uniq() + ".txt": "behind\n"}, "")
	e.pushMain(map[string]string{"it/main-" + e.uniq() + ".txt": "main moved\n"}, "main moves on")
	s.tickUntil("the PR to merge after an update-branch", 15*time.Minute, s.merged)
	if s.pull().HeadSHA == s.head {
		t.Error("the branch was never updated")
	}
	s.assertSquash()
}

func (e *env) caseProtected(t *testing.T) {
	s := e.newScenario(map[string]string{".claude/it-" + e.uniq(): "protected\n"}, "")
	s.tickUntil("ard:needs-approval", 5*time.Minute, func() bool { return s.hasLabel(labels.NeedsApproval) })
	if s.merged() {
		t.Fatal("merged a protected path without approval")
	}
	tok := os.Getenv("ARD_IT_APPROVER_TOKEN")
	if tok == "" {
		t.Log("ARD_IT_APPROVER_TOKEN not set: skipping the approval half")
		return
	}
	approver, err := gogh.NewClient(gogh.WithAuthToken(tok))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := approver.PullRequests.CreateReview(context.Background(), e.repo.Owner, e.repo.Name, s.pr,
		&gogh.PullRequestReviewRequest{CommitID: gogh.Ptr(s.head), Event: gogh.Ptr("APPROVE")}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	s.tickUntil("the approved PR to merge", 12*time.Minute, s.merged)
	s.assertSquash()
}

func (e *env) caseConflict(t *testing.T) {
	u := e.uniq()
	path := "it/conflict-" + u + ".txt"
	root := e.pushMain(map[string]string{path: "original\n"}, "conflict base")
	s := e.newScenario(map[string]string{path: "branch version\n"}, root)
	e.pushMain(map[string]string{path: "main version\n"}, "conflicting main change")
	s.tickUntil("ard:needs-human", 10*time.Minute, func() bool { return s.hasLabel(labels.NeedsHuman) })
	if s.merged() {
		t.Fatal("merged a conflicting PR")
	}
}

func (e *env) caseRedCheck(t *testing.T) {
	if os.Getenv("ARD_IT_CI") == "" {
		t.Skip("ARD_IT_CI not set: the repo has no CI and ruleset (see the test's doc comment)")
	}
	s := e.newScenario(map[string]string{"ci-fail": "fails the scratch CI\n"}, "")
	s.tickUntil("ard:needs-human", 12*time.Minute, func() bool { return s.hasLabel(labels.NeedsHuman) })
	if s.merged() {
		t.Fatal("merged a PR with a red required check")
	}
}
