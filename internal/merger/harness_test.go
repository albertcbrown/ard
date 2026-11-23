package merger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

const (
	self     = "ard[bot]"
	reviewer = "eng-review"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

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

// harness wires the loop to the GitHub fake, a real bare remote with a clone
// as the controller's repository, and an in-memory-ish state store.
type harness struct {
	t      *testing.T
	gh     *fake.Fake
	fx     effects.Effects
	store  *state.Store
	loop   *Loop
	cfg    *config.Resolved
	bare   string
	clone  string
	now    time.Time
	stop   chan struct{}
	ledger string
	logs   *syncBuf
}

type option func(*harness)

func dryRun(h *harness) { h.ledger = filepath.Join(h.t.TempDir(), "effects.jsonl") }

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	h := &harness{t: t, stop: make(chan struct{}), now: t0, logs: &syncBuf{},
		gh: fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}))}
	h.bare, h.clone = gittest.NewRemote(t)
	h.cfg = &config.Resolved{Repo: t.TempDir(), SHA: "abc123",
		ProtectedPaths: []string{".github/", ".claude/", ".ard/config.yaml"},
		Roles:          []config.Role{{Kind: config.KindReviewer, Name: reviewer}}}
	for _, o := range opts {
		o(h)
	}
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st
	if h.ledger != "" {
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: h.ledger})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		h.fx = d
	} else {
		h.fx = effects.NewLive(h.gh, gitx.New(""), quiet)
	}
	h.loop = New(Deps{Reader: h.gh, Effects: h.fx, Store: st, Git: gitx.New(""), RepoDir: h.clone, RemoteURL: h.bare,
		Now: func() time.Time { return h.now }, Log: quiet})
	return h
}

// git runs git in the clone.
func (h *harness) git(args ...string) string {
	h.t.Helper()
	return strings.TrimSpace(gittest.Run(h.t, h.clone, args...))
}

// commit writes path on branch (created from main when it does not exist),
// commits and pushes it, and returns the new tip.
func (h *harness) commit(branch, path, body string) string {
	h.t.Helper()
	if strings.TrimSpace(gittest.Run(h.t, h.clone, "branch", "--list", branch)) == "" {
		h.git("checkout", "-q", "-b", branch, "origin/main")
	} else {
		h.git("checkout", "-q", branch)
	}
	full := filepath.Join(h.clone, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.git("add", path)
	h.git("commit", "-q", "-m", "change "+path)
	h.git("push", "-q", "-f", h.bare, branch)
	if branch == "main" {
		h.git("fetch", "-q", h.bare, "+refs/heads/main:refs/remotes/origin/main")
	}
	return h.git("rev-parse", "HEAD")
}

// updateBranch merges main into branch as GitHub's update-branch does and
// moves the PR's head in the fake.
func (h *harness) updateBranch(pr int, branch string) string {
	h.t.Helper()
	h.git("checkout", "-q", branch)
	h.git("merge", "-q", "--no-edit", "origin/main")
	h.git("push", "-q", "-f", h.bare, branch)
	sha := h.git("rev-parse", "HEAD")
	h.gh.SetHeadSHA(pr, sha)
	return sha
}

// pr is a seeded worker PR.
type pr struct {
	n, issue int
	branch   string
	head     string
	jobID    string
}

type spec struct {
	files    map[string]string // path -> body committed on the branch
	labels   []string
	draft    bool
	author   string
	body     string
	title    string
	mergeSt  string
	noJob    bool
	noClaim  bool
	headRef  string // overrides the branch name
	issueLbl []string
}

// newPR cuts worker/engineer-<issue>-fix from main with a code file and the
// close-out file, seeds the issue, the PR (ready, reviewed at its head), the
// worker job and the review claim.
func (h *harness) newPR(mut ...func(*spec)) pr {
	h.t.Helper()
	s := &spec{files: map[string]string{}, author: self, mergeSt: "clean", title: "feat: fix it (#1)",
		labels: []string{labels.Request(labels.StageReview, reviewer), labels.Done(labels.StageReview, reviewer)}}
	for _, m := range mut {
		m(s)
	}
	issue := h.gh.SeedIssue(github.Issue{Title: "Fix it", Body: "broken", Author: "human", State: github.StateOpen, Labels: s.issueLbl, CreatedAt: t0, UpdatedAt: t0})
	p := pr{issue: issue, branch: "worker/engineer-" + strconv.Itoa(issue) + "-fix"}
	if s.headRef != "" {
		p.branch = s.headRef
	}
	if len(s.files) == 0 {
		s.files["code_"+strconv.Itoa(issue)+".go"] = "package x\n"
	}
	s.files[".ard/"+strconv.Itoa(issue)+"/worker-engineer.md"] = "done\n"
	for path, body := range s.files {
		p.head = h.commit(p.branch, path, body)
	}
	body := s.body
	if body == "" {
		body = "Closes #" + strconv.Itoa(issue) + "\n\nOpened by ard."
	}
	p.n = h.gh.SeedPR(github.PullRequest{Title: s.title, Body: body, State: github.StateOpen, Draft: s.draft, Author: s.author,
		Labels: s.labels, HeadRef: p.branch, HeadSHA: p.head, BaseRef: "main", MergeableState: s.mergeSt})
	if !s.noClaim {
		data, _ := json.Marshal(map[string]string{"head": p.head})
		if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventClaim, Issue: issue, Stage: "review", Role: reviewer, Data: data}); err != nil {
			h.t.Fatal(err)
		}
	}
	if !s.noJob {
		j, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: "engineer", Issue: issue, Branch: p.branch})
		if err != nil {
			h.t.Fatal(err)
		}
		if err := h.store.SetCloseout(context.Background(), j.ID, "closed", json.RawMessage(`{}`)); err != nil {
			h.t.Fatal(err)
		}
		if err := h.store.Complete(context.Background(), j.ID, nil); err != nil {
			h.t.Fatal(err)
		}
		p.jobID = j.ID
	}
	h.git("checkout", "-q", "main")
	return p
}

func (h *harness) tick() (string, error) {
	h.t.Helper()
	buf := &syncBuf{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	err := h.loop.Tick(context.Background(), controller.Env{Log: log, Cfg: h.cfg, Inflight: controller.NewInflight(context.Background(), quiet), Stopping: h.stop})
	out := buf.String()
	h.logs.Write([]byte(out))
	return out, err
}

func (h *harness) mustTick() string {
	h.t.Helper()
	out, err := h.tick()
	if err != nil {
		h.t.Fatalf("tick: %v\n%s", err, out)
	}
	return out
}

// settle ticks once so the loop sees every head, then lets the no-CI grace
// period pass.
func (h *harness) settle() {
	h.t.Helper()
	h.mustTick()
	h.now = h.now.Add(3 * time.Minute)
}

func (h *harness) calls(method string) []fake.Call {
	var out []fake.Call
	for _, c := range h.gh.Writes() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (h *harness) labelsOf(n int) []string {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is.Labels
}

func (h *harness) has(n int, label string) bool {
	for _, l := range h.labelsOf(n) {
		if l == label {
			return true
		}
	}
	return false
}

func (h *harness) comments(n int) []github.Comment {
	h.t.Helper()
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return cs
}

func (h *harness) merged(n int) bool {
	h.t.Helper()
	p, err := h.gh.GetPR(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return p.Merged
}

func (h *harness) issueOpen(n int) bool {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is.State == github.StateOpen
}
