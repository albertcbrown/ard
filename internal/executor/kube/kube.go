// Package kube is the Kubernetes executor: it runs one agent job as a pod
// (init container pre, agent, result server post), watches it through the API,
// pulls the result back and removes everything again. The pod side is
// internal/jobpod's contract; the API is the minimal client in api/.
package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/jobpod"
	"github.com/albertcbrown/ard/internal/tarball"
)

// Kind is the Handle.Kind of a job this executor runs.
const Kind = "k8s"

// Tokens mints and revokes the read token the pre container clones with.
type Tokens interface {
	MintReadToken(ctx context.Context, repos []github.Repo) (github.ScopedToken, error)
	RevokeToken(ctx context.Context, token string) error
}

// Options configures an Executor.
type Options struct {
	Client                 api.Client
	Tokens                 Tokens
	WS                     *workspace.Workspace
	Namespace              string
	Instance               string // app.kubernetes.io/instance label value
	Repo                   string // owner/name this executor serves; Sweep selects on it
	AgentImage, PodImage   string
	GitBase                string // default https://github.com
	Resources              Resources
	Limits                 tarball.Limits
	PreBudget, ResultGrace time.Duration // defaults 5m and 2m
	Policy                 executor.EnvPolicy
	ConfigDirEnv           string // env name whose Fixed value must be the pod config dir
	HTTP                   *http.Client
	// RoleImages turns on in-cluster role image builds; nil keeps role
	// Dockerfiles ignored (the job runs on AgentImage). RoleTree reads the
	// role directory from the controller clone (default: git).
	RoleImages *RoleImages
	RoleTree   RoleTree
	Now        func() time.Time
	Log        *slog.Logger

	// Poll is the watcher interval (default 2s); PendingTimeout is how long a
	// pod may stay unscheduled (default 5m). Tests shorten both.
	Poll, PendingTimeout time.Duration
}

type liveJob struct {
	manifest []byte
	repo     string // owner/name of the job's repository
	started  bool
	uid      string
	key      string // result key, in memory only
	stop     context.CancelFunc
	wg       sync.WaitGroup // the watcher and the log follower
	// agentEnded is set once the agent container is seen terminated or the
	// job is finished; the log follower drains once more after it.
	agentEnded atomic.Bool
	token      string
	tokenGone  bool
}

// Executor implements executor.Executor on Kubernetes.
type Executor struct {
	o    Options
	mu   sync.Mutex
	live map[string]*liveJob
	// builds is the single-flight map of role image builds, by repo and tag.
	builds map[string]*buildCall
}

var _ executor.Executor = (*Executor)(nil)

// New validates o and applies defaults.
func New(o Options) (*Executor, error) {
	switch {
	case o.Client == nil, o.Tokens == nil, o.WS == nil:
		return nil, errors.New("kube: Client, Tokens and WS are required")
	case o.Namespace == "", o.Repo == "", o.AgentImage == "", o.PodImage == "", o.Instance == "":
		return nil, errors.New("kube: Namespace, Repo, Instance, AgentImage and PodImage are required")
	}
	if o.GitBase == "" {
		o.GitBase = "https://github.com"
	}
	if o.PreBudget <= 0 {
		o.PreBudget = 5 * time.Minute
	}
	if o.ResultGrace <= 0 {
		o.ResultGrace = 2 * time.Minute
	}
	if o.Poll <= 0 {
		o.Poll = 2 * time.Second
	}
	if o.PendingTimeout <= 0 {
		o.PendingTimeout = 5 * time.Minute
	}
	if o.Limits == (tarball.Limits{}) {
		o.Limits = tarball.DefaultLimits()
	}
	if o.Resources.Work == 0 {
		o.Resources = DefaultResources()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.RoleImages != nil {
		ri := *o.RoleImages
		if ri.Namespace == "" || ri.BuilderImage == "" || ri.Repository == "" || ri.PushSecret == "" || ri.BuilderUID <= 0 {
			return nil, errors.New("kube: RoleImages needs Namespace, BuilderImage, BuilderUID, Repository and PushSecret")
		}
		if err := executor.CheckImageRef(ri.BuilderImage); err != nil {
			return nil, err
		}
		if !strings.Contains(ri.Repository, "/") {
			return nil, errors.New("kube: RoleImages.Repository must be <registry>/<path>")
		}
		ri.Repository = strings.TrimSuffix(ri.Repository, "/")
		if ri.AgentBase == "" {
			ri.AgentBase = DefaultAgentBase
		}
		if ri.Budget <= 0 {
			ri.Budget = 15 * time.Minute
		}
		if ri.MaxContext <= 0 {
			ri.MaxContext = 512 << 10
		}
		o.RoleImages = &ri
		if o.RoleTree == nil {
			o.RoleTree = ExecTree("")
		}
	}
	return &Executor{o: o, live: map[string]*liveJob{}, builds: map[string]*buildCall{}}, nil
}

func (e *Executor) get(id string) *liveJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.live[id]
}

// Prepare lays out the worktree and encodes the manifest. It starts nothing.
func (e *Executor) Prepare(ctx context.Context, j executor.Job) (executor.Handle, error) {
	if len(j.ID) > jobpod.MaxJobID {
		return executor.Handle{}, fmt.Errorf("%w: id %q is too long for a pod name", executor.ErrInvalidJob, j.ID)
	}
	if j.RoleSource.SHA == "" || j.RoleSource.Path == "" {
		return executor.Handle{}, fmt.Errorf("%w: no role source (a config without a commit cannot run on Kubernetes)", executor.ErrInvalidJob)
	}
	image := e.o.AgentImage
	if j.Dockerfile != "" {
		if e.o.RoleImages == nil {
			e.o.Log.Info("kube.dockerfile_ignored", "job", j.ID, "dockerfile", j.Dockerfile)
		} else {
			// Built from the pinned role SHA in the controller clone, before the
			// worktree exists so a build failure leaves nothing to discard.
			ref, err := e.roleImage(ctx, j)
			if err != nil {
				return executor.Handle{}, err
			}
			image = ref
		}
	}
	h, err := e.o.WS.Prepare(ctx, j)
	if err != nil {
		return executor.Handle{}, err
	}
	h.Kind, h.Container, h.Image = Kind, jobpod.PodName(j.ID), image
	h.View = jobpod.AgentPaths()
	if h.SidecarDir == "" {
		h.View.Sidecar = ""
	}
	if h.HistoryDir == "" {
		h.View.History = ""
	}
	m := jobpod.Manifest{V: jobpod.ManifestVersion, Repo: j.Repo, BaseSHA: h.BaseSHA,
		History: j.History, ReadOnly: j.ReadOnly,
		Role:  jobpod.Source{Repo: j.Repo, SHA: j.RoleSource.SHA, Path: j.RoleSource.Path},
		Scrub: j.Scrub, Inputs: j.Inputs}
	if h.SidecarDir != "" {
		m.Sidecar = &jobpod.Source{Repo: h.Sidecar.Repo, SHA: h.SidecarSHA}
	}
	b, err := jobpod.Encode(m)
	if err != nil {
		e.o.WS.Discard(context.WithoutCancel(ctx), h)
		if errors.Is(err, jobpod.ErrTooLarge) {
			return executor.Handle{}, fmt.Errorf("%w: %w", executor.ErrInputsTooLarge, err)
		}
		return executor.Handle{}, fmt.Errorf("%w: %w", executor.ErrInvalidJob, err)
	}
	e.mu.Lock()
	e.live[j.ID] = &liveJob{manifest: b, repo: j.Repo}
	e.mu.Unlock()
	return h, nil
}

// Start creates the pod and its Secret and starts the watcher and the log
// follower. ctx bounds only the API calls.
func (e *Executor) Start(ctx context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	if h.Kind != Kind {
		return h, fmt.Errorf("%w: handle kind %q", executor.ErrInvalidJob, h.Kind)
	}
	if c.Stdin != nil {
		return h, fmt.Errorf("%w: stdin is not supported on Kubernetes", executor.ErrInvalidJob)
	}
	if err := e.o.Policy.CheckEnv(c.Env); err != nil {
		return h, err
	}
	if err := e.o.Policy.CheckFixed(c.Fixed); err != nil {
		return h, err
	}
	for _, kv := range c.Fixed {
		if n, v, _ := strings.Cut(kv, "="); e.o.ConfigDirEnv != "" && n == e.o.ConfigDirEnv && v != jobpod.AgentConfig {
			return h, fmt.Errorf("%w: %s must be %s", executor.ErrForbiddenEnv, n, jobpod.AgentConfig)
		}
	}
	e.mu.Lock()
	lj := e.live[h.JobID]
	if lj != nil && lj.started {
		e.mu.Unlock()
		return h, executor.ErrAlreadyStarted
	}
	e.mu.Unlock()
	if lj == nil {
		return h, executor.ErrNotPrepared
	}
	now := e.o.Now().UTC()
	h.StartedAt = now
	h.Deadline = now.Add(e.o.PreBudget + h.Timeout)

	repos, err := githubRepos(lj.repo, h)
	if err != nil {
		return h, err
	}
	tok, err := e.o.Tokens.MintReadToken(ctx, repos)
	if err != nil {
		return h, fmt.Errorf("mint read token: %w", err)
	}
	revoke := func() { e.o.Tokens.RevokeToken(context.WithoutCancel(ctx), tok.Value) }
	key, err := jobpod.NewKey()
	if err != nil {
		revoke()
		return h, err
	}
	pod := buildPod(h, c, e.o)
	created, err := e.o.Client.CreatePod(ctx, e.o.Namespace, pod)
	if err != nil {
		revoke()
		return h, fmt.Errorf("create pod: %w", err)
	}
	sec, err := buildSecret(h, c, lj.manifest, tok.Value, key, created.Meta.UID)
	if err == nil {
		err = e.o.Client.CreateSecret(ctx, e.o.Namespace, sec)
	}
	if err != nil {
		e.o.Client.DeletePod(context.WithoutCancel(ctx), e.o.Namespace, pod.Meta.Name, 0)
		revoke()
		return h, fmt.Errorf("create secret: %w", err)
	}
	wctx, stop := context.WithCancel(context.Background())
	e.mu.Lock()
	lj.started, lj.uid, lj.key, lj.token, lj.stop = true, created.Meta.UID, key, tok.Value, stop
	lj.wg.Add(2)
	e.mu.Unlock()
	go func() {
		defer lj.wg.Done()
		e.watch(wctx, h, lj)
	}()
	go func() {
		defer lj.wg.Done()
		e.followLogs(wctx, h, lj)
	}()
	return h, nil
}

func githubRepos(repo string, h executor.Handle) ([]github.Repo, error) {
	var out []github.Repo
	for _, r := range []string{repo, h.Sidecar.Repo} {
		if r == "" {
			continue
		}
		o, n, ok := strings.Cut(r, "/")
		if !ok {
			return nil, fmt.Errorf("%w: repo %q", executor.ErrInvalidJob, r)
		}
		out = append(out, github.Repo{Owner: o, Name: n})
	}
	return out, nil
}

// Status reports the job's state from the controller's files and the pod.
func (e *Executor) Status(ctx context.Context, h executor.Handle) (executor.Status, error) {
	o := obs{now: e.o.Now()}
	if h.Kind == Kind && !h.StartedAt.IsZero() {
		d, err := readDone(h)
		if err != nil {
			return executor.Status{}, err
		}
		o.done = d
		if d == nil {
			o.pod, o.podErr = e.o.Client.GetPod(ctx, e.o.Namespace, jobpod.PodName(h.JobID))
		}
	}
	if lj := e.get(h.JobID); lj != nil {
		e.mu.Lock()
		o.held = lj.started
		e.mu.Unlock()
		if o.held && errors.Is(o.podErr, api.ErrNotFound) {
			// The watcher sees the same and writes the marker with the real
			// reason within one poll; do not report Gone before it has.
			o.podErr = nil
		}
	}
	if _, err := os.Stat(h.Worktree); err == nil {
		o.wtExists = true
	}
	if st, err := os.Stat(h.LogPath); err == nil {
		o.lastOut = st.ModTime()
	}
	return mapStatus(h, o), nil
}

// Logs reads the local log files, which the follower and the result pull fill.
func (e *Executor) Logs(ctx context.Context, h executor.Handle, o executor.LogsOptions) (io.ReadCloser, error) {
	p := h.LogPath
	if o.Stream == executor.Stdout {
		p = h.StdoutPath
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	if o.TailLines > 0 {
		lines := strings.SplitAfter(string(b), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > o.TailLines {
			lines = lines[len(lines)-o.TailLines:]
		}
		b = []byte(strings.Join(lines, ""))
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

// Cleanup stops the watcher, deletes the pod and Secret, revokes a held token
// and removes the job's directories. It never removes logs and is idempotent.
func (e *Executor) Cleanup(ctx context.Context, h executor.Handle, o executor.CleanupOptions) error {
	ctx = context.WithoutCancel(ctx)
	var errs []error
	e.mu.Lock()
	lj := e.live[h.JobID]
	delete(e.live, h.JobID)
	e.mu.Unlock()
	if lj != nil && lj.stop != nil {
		lj.stop()
		lj.wg.Wait()
	}
	if h.Kind == Kind {
		name := jobpod.PodName(h.JobID)
		if err := e.o.Client.DeletePod(ctx, e.o.Namespace, name, 30); err != nil {
			errs = append(errs, err)
		}
		if err := e.o.Client.DeleteSecret(ctx, e.o.Namespace, name); err != nil {
			errs = append(errs, err)
		}
		if lj != nil {
			if err := e.revoke(ctx, lj); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if o.KeepWorktree {
		return errors.Join(errs...)
	}
	for _, p := range []string{h.Worktree + ".input", h.Worktree + ".result", h.Worktree + ".done", h.Worktree + ".done.tmp"} {
		if err := workspace.RemoveAllWritable(p); err != nil {
			errs = append(errs, err)
		}
	}
	if h.Kind != Kind {
		// A handle of another executor: its directories only.
		workspace.ChmodWritable(h.Worktree)
		errs = append(errs, workspace.RemoveAllWritable(h.Worktree), workspace.RemoveAllWritable(h.Home))
		return errors.Join(errs...)
	}
	// The result swap moved the worktree, so `git worktree remove` cannot
	// find it: remove the directories, then prune the registration.
	workspace.ChmodWritable(h.Worktree)
	errs = append(errs, workspace.RemoveAllWritable(h.Worktree), workspace.RemoveAllWritable(h.Home))
	if err := e.o.WS.Remove(ctx, h); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// revoke revokes the job's token once.
func (e *Executor) revoke(ctx context.Context, lj *liveJob) error {
	e.mu.Lock()
	tok, gone := lj.token, lj.tokenGone
	lj.tokenGone = true
	e.mu.Unlock()
	if gone || tok == "" {
		return nil
	}
	return e.o.Tokens.RevokeToken(ctx, tok)
}

// Prune deletes host log files older than olderThan, as the local executor does.
func (e *Executor) Prune(ctx context.Context, olderThan time.Duration) error {
	return e.o.WS.Prune(ctx, olderThan)
}

// Secrets returns the live read-token values, for the redaction set.
func (e *Executor) Secrets() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, lj := range e.live {
		if lj.token != "" && !lj.tokenGone {
			out = append(out, lj.token)
		}
	}
	return out
}

// Sweep deletes this repo's job pods and Secrets whose job keep rejects. It
// touches only objects labelled for Options.Repo.
func (e *Executor) Sweep(ctx context.Context, keep func(id string) bool) error {
	sel := jobpod.LabelPartOf + "=ard," + jobpod.LabelComponent + "=" + jobpod.ComponentJob +
		"," + jobpod.LabelInstance + "=" + jobpod.LabelValue(e.o.Instance) + "," + jobpod.LabelRepo + "=" + jobpod.RepoLabel(e.o.Repo)
	pods, err := e.o.Client.ListPods(ctx, e.o.Namespace, sel)
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	var errs []error
	for _, p := range pods {
		if keep(p.Meta.Labels[jobpod.LabelJob]) {
			continue
		}
		errs = append(errs, e.o.Client.DeletePod(ctx, e.o.Namespace, p.Meta.Name, 30),
			e.o.Client.DeleteSecret(ctx, e.o.Namespace, p.Meta.Name))
	}
	return errors.Join(errs...)
}
