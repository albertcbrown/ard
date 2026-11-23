package closeout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

var (
	quiet   = slog.New(slog.NewTextHandler(io.Discard, nil))
	errBoom = errors.New("boom")
)

// secretValue is a known secret the redactor must strip.
const secretValue = "s3cr3t-value-0123456789"

// planted returns a token that trips the built-in scanner, built at run time
// so no token-shaped literal is committed.
func planted() string { return "gh" + "s_" + strings.Repeat("aB3xZ9", 7) }

// flakyEffects counts effect and commit calls on one shared counter and fails
// the failAt-th (1-based; 0 = never).
type flakyEffects struct {
	inner  Effects
	mu     *sync.Mutex
	n      *int
	failAt int
}

func (f flakyEffects) tick() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	*f.n++
	if f.failAt != 0 && *f.n == f.failAt {
		return errBoom
	}
	return nil
}
func (f flakyEffects) AddLabels(ctx context.Context, n int, l ...string) error {
	if err := f.tick(); err != nil {
		return err
	}
	return f.inner.AddLabels(ctx, n, l...)
}
func (f flakyEffects) RemoveLabel(ctx context.Context, n int, l string) error {
	if err := f.tick(); err != nil {
		return err
	}
	return f.inner.RemoveLabel(ctx, n, l)
}
func (f flakyEffects) CreateComment(ctx context.Context, n int, b string) (github.Comment, error) {
	if err := f.tick(); err != nil {
		return github.Comment{}, err
	}
	return f.inner.CreateComment(ctx, n, b)
}
func (f flakyEffects) CreateReview(ctx context.Context, n int, in github.ReviewInput) (int64, error) {
	if err := f.tick(); err != nil {
		return 0, err
	}
	return f.inner.CreateReview(ctx, n, in)
}
func (f flakyEffects) DryRun() bool { return f.inner.DryRun() }

// fakeCommitter records commits. An identical file set returns the same SHA.
type fakeCommitter struct {
	mu       sync.Mutex
	counter  *int
	failAt   int
	err      error // returned by every call when set (permanent errors)
	calls    int
	commits  map[string]map[string]string // sha -> path -> content ("" + deleted marker)
	branches []string
	reqs     []committer.Request
}

func newFakeCommitter() *fakeCommitter {
	return &fakeCommitter{commits: map[string]map[string]string{}}
}

func (c *fakeCommitter) Commit(ctx context.Context, branch string, req committer.Request) (committer.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.counter != nil {
		*c.counter++
		if c.failAt != 0 && *c.counter == c.failAt {
			return committer.Result{}, fmt.Errorf("commit interrupted: %w", context.Canceled)
		}
	}
	if c.err != nil {
		return committer.Result{}, c.err
	}
	c.reqs = append(c.reqs, req)
	c.branches = append(c.branches, branch)
	files := map[string]string{}
	var keys []string
	for _, f := range req.Files {
		v := "\x00deleted"
		if !f.Delete {
			b, err := os.ReadFile(f.Src)
			if err != nil {
				return committer.Result{}, err
			}
			v = string(b)
		}
		files[f.Path] = v
		keys = append(keys, f.Path+"\x00"+v)
	}
	sort.Strings(keys)
	h := sha256.Sum256([]byte(branch + "\x01" + strings.Join(keys, "\x01")))
	sha := hex.EncodeToString(h[:])[:40]
	if _, ok := c.commits[sha]; ok {
		return committer.Result{SHA: sha, NoOp: true}, nil
	}
	c.commits[sha] = files
	return committer.Result{SHA: sha}, nil
}

// flakyStore wraps a state.Store and fails the failAt-th SetCloseout.
type flakyStore struct {
	*state.Store
	n      int
	failAt int
}

func (s *flakyStore) SetCloseout(ctx context.Context, id, phase string, plan json.RawMessage) error {
	s.n++
	if s.failAt != 0 && s.n == s.failAt {
		return errBoom
	}
	return s.Store.SetCloseout(ctx, id, phase, plan)
}

type env struct {
	t       *testing.T
	repo    string // worktree: a clone with .ard/<issue>/issue.md committed
	base    string
	staging string
	logDir  string
	gh      *fake.Fake
	store   *state.Store
	fstore  *flakyStore
	cm      *fakeCommitter
	calls   int
	d       Deps
	issue   int
	pr      int
	secrets *redact.Secrets
}

func newEnv(t *testing.T) *env {
	t.Helper()
	_, clone := gittest.NewRemote(t)
	e := &env{t: t, repo: clone, staging: t.TempDir(), logDir: t.TempDir(), gh: fake.New(), cm: newFakeCommitter(), issue: 7, pr: 9}
	if err := os.MkdirAll(filepath.Join(clone, ".ard", "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.write(".ard/7/issue.md", "the ask")
	e.write(".ard/7/other.md", "untouched")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "base")
	e.base = strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))

	e.gh.SeedIssue(github.Issue{Number: 7, Title: "t", State: github.StateOpen, Labels: []string{"ard:pending", "ard:triage:lead:running", "ard:worker:engineer:running"}})
	e.gh.SeedPR(github.PullRequest{Number: 9, Title: "p", State: github.StateOpen, Labels: []string{"ard:review:eng-review:running", "ard:review:eng-review:changes"}})

	st, err := state.Open(context.Background(), t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.store = st
	e.fstore = &flakyStore{Store: st}
	e.secrets = redact.NewSecrets()
	if err := e.secrets.Add("MODEL_KEY", secretValue); err != nil {
		t.Fatal(err)
	}
	e.cm.counter = &e.calls
	e.d = e.deps(0)
	return e
}

// deps builds Deps whose failAt-th shared call (effects and commit) fails.
func (e *env) deps(failAt int) Deps {
	e.cm.failAt = failAt
	live := effects.NewLive(e.gh, gitx.New(""), quiet)
	return Deps{
		Effects:   flakyEffects{inner: live, mu: &sync.Mutex{}, n: &e.calls, failAt: failAt},
		Comments:  e.gh,
		Committer: e.cm,
		Store:     e.fstore,
		Roles:     testRoles,
		NewRedactor: func(Job) (Redactor, error) {
			return redact.New(e.secrets, redact.Options{})
		},
		Changed: func(ctx context.Context, wt, base string, paths ...string) ([]gitx.PathChange, error) {
			return gitx.New("").ChangedSince(ctx, e.repo, wt, base, paths...)
		},
		BaseFile:   gitx.New("").FileAt,
		Repo:       github.Repo{Owner: "o", Name: "r"},
		StagingDir: e.staging,
		Log:        quiet,
		Now:        func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	}
}

func (e *env) write(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// commitAll commits the clone's working tree and returns the new HEAD, so a
// test can put files at the job's base: set j.BaseSHA to the result.
func (e *env) commitAll(msg string) string {
	e.t.Helper()
	gittest.Run(e.t, e.repo, "add", "-A")
	gittest.Run(e.t, e.repo, "commit", "-m", msg)
	e.base = strings.TrimSpace(gittest.Run(e.t, e.repo, "rev-parse", "HEAD"))
	return e.base
}

// scrub does what executor.Job.Scrub does before the agent runs and records
// it in j.Scrubbed, as the handle does.
func (e *env) scrub(j *Job, rel string) {
	e.t.Helper()
	if err := os.Remove(filepath.Join(e.repo, filepath.FromSlash(rel))); err != nil {
		e.t.Fatal(err)
	}
	j.Scrubbed = append(j.Scrubbed, rel)
}

func roleFile(role, status string, extra ...string) string {
	return "---\nrole: " + role + "\nstatus: " + status + "\nattempt: 1\n" + strings.Join(extra, "\n") + "\n---\nSummary of the work.\n"
}

// claim creates the state job and returns the close-out Job for it.
func (e *env) claim(stage, role string) Job {
	e.t.Helper()
	nj := state.NewJob{Stage: stage, Role: role, Issue: e.issue}
	j := Job{Stage: stage, Role: role, Issue: e.issue, Worktree: e.repo, BaseSHA: e.base}
	if stage != StageTriage {
		j.Branch = "worker/engineer-x"
	}
	if stage == StageReview {
		nj.PR, j.PR = e.pr, e.pr
	}
	sj, err := e.store.CreateJob(context.Background(), nj)
	if err != nil {
		e.t.Fatal(err)
	}
	if j.Attempt, err = e.store.StartAttempt(context.Background(), sj.ID); err != nil {
		e.t.Fatal(err)
	}
	j.ID = sj.ID
	logPath := filepath.Join(e.logDir, stage+"-"+role+"-20260102T030405Z.log")
	if err := os.WriteFile(logPath, []byte("agent output\nkey="+secretValue+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	j.LogPath = logPath
	return j
}

func (e *env) labels(n int) map[string]bool {
	e.t.Helper()
	m := map[string]bool{}
	is, err := e.gh.GetIssue(context.Background(), n)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, l := range is.Labels {
		m[l] = true
	}
	return m
}

func (e *env) markerComments(n int, id string) int {
	e.t.Helper()
	cs, err := e.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		e.t.Fatal(err)
	}
	c := 0
	for _, cm := range cs {
		if strings.HasPrefix(cm.Body, markerFor(id)) {
			c++
		}
	}
	return c
}

func (e *env) sjob(id string) state.Job {
	e.t.Helper()
	j, err := e.store.Job(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

func (e *env) markerReviews(n int, id string) int {
	e.t.Helper()
	rs, err := e.gh.ListReviews(context.Background(), n)
	if err != nil {
		e.t.Fatal(err)
	}
	c := 0
	for _, r := range rs {
		if strings.HasPrefix(r.Body, markerFor(id)) {
			c++
		}
	}
	return c
}

// reviewCalls returns the CreateReview and CreateComment calls the fake saw.
func (e *env) reviewCalls() (reviews []github.ReviewInput, comments int) {
	for _, c := range e.gh.Writes() {
		switch c.Method {
		case "CreateReview":
			reviews = append(reviews, c.Args[1].(github.ReviewInput))
		case "CreateComment":
			comments++
		}
	}
	return reviews, comments
}
