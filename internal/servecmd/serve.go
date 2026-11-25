// Package servecmd implements `ard serve`: it resolves the target repo,
// takes the leader lock, opens state, builds the GitHub reader and effect
// layer, adopts running jobs into the slot pool, and runs the controller.
// Loops are registered in registerLoops, one constructor call each.
package servecmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/budget"
	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/configsrc"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/costs"
	"github.com/albertcbrown/ard/internal/digest"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/executor/local"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/janitor"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/prwatch"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/repopath"
	"github.com/albertcbrown/ard/internal/scanner/stale"
	"github.com/albertcbrown/ard/internal/snapshot"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// deps is what serve needs from the outside world. Tests inject fakes
// through build, so no test builds a real GitHub client.
type deps struct {
	Reader    github.Reader
	Effects   effects.Effects
	RemoteURL string         // where the controller fetches main from
	Token     gitx.TokenFunc // mints a push and fetch credential; nil in tests
	Close     func() error   // optional
	// Model is the credential agent jobs receive. The zero value means none
	// is set: deterministic scanners run, and a job that needs one fails
	// through Model.Require.
	Model modelauth.Credential
	// Auth is the GitHub credential; the k8s executor mints scoped read
	// tokens from it. nil in tests that do not need one.
	Auth github.Auth
}

// builder constructs deps for repo. stateDir is where a dry-run ledger goes.
type builder func(ctx context.Context, e *cli.Env, repo github.Repo, stateDir string) (deps, error)

// gitBin is the git binary the version check runs; empty means "git". Tests override it.
var gitBin = ""

// Serve is the handler behind `ard serve`.
func Serve(ctx context.Context, e *cli.Env, o cli.ServeOpts) error {
	return serveLoops(ctx, e, buildDeps, o.Loops, o.K8s, o.UI)
}

func serve(ctx context.Context, e *cli.Env, build builder) error {
	return serveLoops(ctx, e, build, nil, cli.K8sOpts{}, uiOff)
}

// loopNames lists the names of loops, for the serve.loops log line.
func loopNames(loops []controller.Loop) []string {
	names := make([]string, 0, len(loops))
	for _, l := range loops {
		names = append(names, l.Name())
	}
	return names
}

// knownLoops are the names registerLoops' loops report (Loop.Name), in the
// order they register. "budget" selects the status-issue notice loop; the
// claim gate itself is built into the pool whatever --loops says.
var knownLoops = []string{"scanners", stale.Name, "triage", "workers", "reviewers", "reviewfix", "merger", prwatch.Name, janitor.Name, "reaper", "budget", digest.Name, controller.ApprovalsName}

// selectLoops keeps the loops named in want, in registration order; an empty
// want keeps every loop. A name that is not a known loop is a usage error
// (exit 2) naming the known ones, so a typo never silently drops a loop.
func selectLoops(loops []controller.Loop, want []string) ([]controller.Loop, error) {
	if len(want) == 0 {
		return loops, nil
	}
	keep := map[string]bool{}
	for _, n := range want {
		n = strings.TrimSpace(n)
		if !slices.Contains(knownLoops, n) {
			return nil, &cli.ExitError{Code: 2, Err: fmt.Errorf("ard serve: --loops: unknown loop %q (known: %s)", n, strings.Join(knownLoops, ", "))}
		}
		keep[n] = true
	}
	var out []controller.Loop
	for _, l := range loops {
		if keep[l.Name()] {
			out = append(out, l)
		}
	}
	return out, nil
}

// dockerProbe checks that the Docker daemon answers. A variable so tests can
// fake it. It uses the same "docker" binary name as local.Options.DockerBin's default.
var dockerProbe = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
}

func serveLoops(ctx context.Context, e *cli.Env, build builder, wantLoops []string, k8s cli.K8sOpts, uiMode string) error {
	if _, err := selectLoops(nil, wantLoops); err != nil {
		return err
	}
	root, err := config.RepoRoot(e.ConfigPath)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	// root is the operator's checkout, made absolute because RepoRoot returns "." when --config is omitted. It is read only (origin fallback, local paused); git worktrees and commits use the controller clone (#113).
	if root, err = filepath.Abs(root); err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	repo, err := resolveRepo(ctx, e.Repo, root)
	if err != nil {
		return err
	}
	if err := gitx.New(gitBin).CheckVersion(ctx); err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("ard serve: find home directory: %w", err)
	}
	if err := state.CheckLegacy(filepath.Join(home, ".ard"), repo.String()); err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}
	lockPath, err := controller.LockPath(home, repo.Owner, repo.Name)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	leader, err := controller.AcquireLeader(lockPath)
	if err != nil {
		return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
	}
	defer leader.Release()

	// The command center draws on the terminal, so while it runs every log
	// line goes to serve.log instead (#414).
	useUI, err := uiWanted(uiMode, os.Stdin, e.Stdout, os.Getenv)
	if err != nil {
		return &cli.ExitError{Code: 2, Err: fmt.Errorf("ard serve: %w", err)}
	}
	var (
		baseLog = e.Log.Handler()
		errOut  = e.Stderr
		logPath string
	)
	if useUI {
		lf, path, err := openServeLog(home, repo)
		if err != nil {
			return fmt.Errorf("ard serve: open serve log: %w", err)
		}
		defer lf.Close()
		fl, err := cli.NewLogger(e.Globals.LogLevel, lf)
		if err != nil {
			return err
		}
		baseLog, errOut, logPath = fl.Handler(), lf, path
		defer func() { fmt.Fprintln(e.Stderr, "logs:", path) }()
	}

	stateDir, err := state.Dir(filepath.Join(home, ".ard"), repo.String(), e.DryRun)
	if err != nil {
		return err
	}
	store, err := state.Open(ctx, stateDir, state.Options{})
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	defer store.Close()
	scanners := scannerdb.NewRegistry(stateDir, scannerdb.Options{})
	defer scanners.Close()

	d, err := build(ctx, e, repo, stateDir)
	if err != nil {
		return err
	}
	if d.Close != nil {
		defer d.Close()
	}

	// The ring keeps the newest log records for the command center; the tee
	// passes every record to the existing handler unchanged.
	events := snapshot.NewRing(snapshotEvents)
	log := slog.New(snapshot.NewTee(baseLog, events, nil))
	// The controller's own bare clone: every config fetch, committer lane and
	// executor worktree uses it, never the operator's checkout (#113).
	clone, err := openClone(ctx, gitx.New(""), home, repo, d.RemoteURL, d.Token, log)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	// Config comes from origin/main, not from the working tree (#87): a merged
	// change applies on the next tick without a pull, and every job of a tick
	// runs against one commit.
	cacheDir, err := repopath.Join(filepath.Join(home, ".ard", "config"), repo.String())
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	// The shared roles repo (roles_repo) is cached as a bare clone beside the
	// config snapshots, one per consuming repo.
	sharedDir, err := repopath.Join(filepath.Join(home, ".ard", "roles-repo"), repo.String())
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	src, err := configsrc.New(configsrc.Options{
		Git: gitx.New(""), RepoDir: clone, RemoteURL: d.RemoteURL, Token: d.Token,
		CacheDir: cacheDir, SharedDir: sharedDir, Log: log,
	})
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	gate := controller.NewPauseGate(d.Reader, log)
	// root is the operator's checkout: read only (origin fallback, local paused). ard never writes to it (#113).
	gate.SetLocalConfig(root)
	pausePath, err := controller.PauseFilePath(home, repo.Owner, repo.Name)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	gate.SetPauseFile(pausePath)
	// Make `touch ~/.ard/pause/<owner>/<repo>` work on a fresh host.
	if err := os.MkdirAll(filepath.Dir(pausePath), 0o700); err != nil {
		log.Warn("serve.pause_dir_failed", "path", filepath.Dir(pausePath), "error", err)
	}

	agentImage := os.Getenv(EnvAgentImage)
	if agentImage != "" {
		if err := executor.CheckImageRef(agentImage); err != nil {
			return fmt.Errorf("ard serve: %s: %w", EnvAgentImage, err)
		}
		if out, err := dockerProbe(ctx); err != nil {
			return fmt.Errorf("ard serve: %s is set but Docker is not available: %w: %s", EnvAgentImage, err, strings.TrimSpace(string(out)))
		}
		log.Info("serve.agent_image", "image", agentImage)
	}

	w := &wiring{
		AgentImage: agentImage, Log: log, Reader: d.Reader, Effects: d.Effects, Store: store, MaxPods: config.DefaultMaxPods,
		Repo: repo, Root: clone, Home: home, RemoteURL: d.RemoteURL, Token: d.Token, Scanners: scanners, Model: d.Model,
		StateDir: stateDir, Started: time.Now(), K8s: k8s, Auth: d.Auth,
	}
	defer w.closeAll()
	paths, perr := newPathRedactor(w.hostPaths())
	if perr != nil {
		log.Error("serve.config_error_paths", "error", perr)
	}
	loader := &cfgLoader{paths: paths, src: src, fx: d.Effects, log: log, stderr: errOut, agentImage: agentImage != "", issue: func() int { return int(w.statusIssue.Load()) }}

	// An origin/main whose config cannot be loaded does not abort startup:
	// serve starts paused, tells the status issue, and retries every tick.
	loadCtx, cancelLoad := context.WithTimeout(ctx, 2*time.Minute)
	cfg, cfgErr := loader.Load(loadCtx)
	cancelLoad()
	if cfgErr != nil {
		log.Error("serve.config_unavailable", "error", cfgErr, "hint", "starting paused; will retry every tick")
	} else {
		w.MaxPods = cfg.MaxPods
		w.cfg.Store(cfg)
		// A k8s serve that cannot build its executor (a PAT cannot mint
		// scoped tokens, a missing flag) must not start.
		if cfg.Executor == "k8s" {
			if _, err := w.newExecutor(); err != nil {
				return &cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}
			}
		}
	}
	// A scan can outlast a pause, so the scanners re-check after it. The
	// check fails closed, like the gate's own.
	w.Paused = func(ctx context.Context) bool {
		c, err := loader.Load(ctx)
		if err != nil {
			return true
		}
		return gate.Check(ctx, c).Paused
	}

	selected, err := selectLoops(registerLoops(w), wantLoops)
	if err != nil {
		return err
	}
	log.Info("serve.loops", "selected", loopNames(selected))
	cc, err := controller.New(controller.Deps{
		Log: log,
		Config: func(ctx context.Context) (*config.Resolved, error) {
			c, err := loader.Load(ctx)
			if err != nil {
				return nil, err
			}
			if err := w.checkExecutor(c); err != nil {
				return nil, err
			}
			w.cfg.Store(c)
			if p := w.pool.Load(); p != nil {
				p.SetMax(c.MaxPods) // a reload may change max_pods
			}
			return c, nil
		},
		Pause: gate,
		Freed: func() <-chan struct{} {
			if p := w.pool.Load(); p != nil {
				return p.Freed()
			}
			return nil
		},
		DemandLoops: func(t time.Time) []string {
			if p := w.pool.Load(); p != nil {
				return p.DemandLoops(t)
			}
			return nil
		},
		// Order matters: closeout-resume goes first, so a job whose close-out
		// was interrupted is finished (or stays running and is adopted) before
		// the pool is built; #16 needs the pool adopted before the first Offer.
		Startup: []controller.Hook{
			{Name: "closeout-resume", Fn: w.resumeCloseouts},
			{Name: "pool-adopt", Fn: func(ctx context.Context) error {
				var cl workspaceCleaner
				if ex, err := w.newExecutor(); err == nil {
					cl = ex
					if err := sweepOrphans(ctx, ex, store, log); err != nil {
						return err
					}
				}
				if err := failStaleScannerJobs(ctx, store, cl, log); err != nil {
					return err
				}
				if _, err := w.limitsBook(ctx); err != nil {
					return err
				}
				p, err := adoptPool(ctx, store, w.MaxPods, log, w.limitedFor, w.budgetGate().Denied)
				if err != nil {
					return err
				}
				w.pool.Store(p)
				return nil
			}},
			{Name: "status-issue", Fn: func(ctx context.Context) error {
				id, err := d.Reader.Identity(ctx)
				if err != nil {
					return fmt.Errorf("identify github user: %w", err)
				}
				n, err := controller.EnsureStatusIssue(ctx, d.Reader, d.Effects, store, id.Login, log)
				if err != nil {
					return err
				}
				gate.SetStatusIssue(n)
				w.statusIssue.Store(int64(n))
				return nil
			}},
		},
		Force: forceOnSecondSignal(ctx),
		Wake:  wakeSignals(ctx),
	}, selected...)
	if err != nil {
		return fmt.Errorf("ard serve: %w", err)
	}
	w.snap = w.newSnapshotTaker(cc, events)
	w.ctl.Store(cc)
	startSHA := ""
	if cfg != nil {
		startSHA = cfg.SHA
	}
	log.Info("serve.start", "repo", repo.String(), "dry_run", e.DryRun, "pause_file", pausePath, "config_sha", startSHA)
	if useUI {
		return runWithUI(ctx, cc.Run, w.snap.Take, repo.String(), logPath, os.Stdin, e.Stdout)
	}
	return cc.Run(ctx)
}

// wiring holds the long-lived dependencies loops take through their own
// constructors. The pool is not built yet when constructors run (the
// pool-adopt startup hook builds it inside Run), so loops must call Pool()
// at tick time, never in their constructors.
type wiring struct {
	// AgentImage is the default agent image (ARD_AGENT_IMAGE); "" = host mode for roles without a Dockerfile.
	AgentImage string
	Log        *slog.Logger
	Reader     github.Reader
	Effects    effects.Effects
	Store      *state.Store
	MaxPods    int
	Repo       github.Repo
	Root       string // absolute path of the controller's own bare clone (~/.ard/repos/<owner>/<repo>.git); every committer and executor worktree is cut from it. Never the operator's checkout.
	Home       string // the user's home directory; ~/.ard holds state, logs and worktrees
	// RemoteURL is where main is fetched from and pushed to.
	RemoteURL string
	Token     gitx.TokenFunc
	Scanners  *scannerdb.Registry
	Model     modelauth.Credential           // zero when no model credential is set; see deps.Model
	Paused    func(ctx context.Context) bool // nil = never paused
	// bodyLocks serialises issue body read-modify-writes: the scanner pipeline
	// and the costs loop share it, so they never interleave on one body.
	bodyLocks costblock.Locks
	pool      atomic.Pointer[pool.Pool]

	// ctl is the controller, set once it is built (after the loops and the
	// Closer); close-out and the approval watch wake loops through it.
	ctl atomic.Pointer[controller.Controller]

	statusIssue atomic.Int64 // the status issue number; 0 until the startup hook has run

	// StateDir is the per-repo state directory; close-out stages files under it.
	StateDir string
	// Started is when this serve process started: the Reaper's restart cut-off.
	Started time.Time
	// cfg is the latest loaded config, for close-out's role checks and the
	// Reaper's result parsing. nil until the first successful load.
	cfg atomic.Pointer[config.Resolved]

	// initMu guards the lazily built shared parts below: one main committer,
	// one branch committer, one Closer, one executor, one Finisher and Reaper.
	initMu sync.Mutex
	mc     *lazyCommitter
	br     *lazyBranches
	cl     *closeout.Closer
	exec   jobExecutor
	// mode is the executor chosen at the first build ("local" or "k8s"); it
	// is fixed for the life of the process.
	mode string
	// K8s are the cluster settings (--k8s-*); Auth mints the pods' read tokens.
	K8s  cli.K8sOpts
	Auth github.Auth
	// newKube builds the cluster client; tests replace it with a fake.
	// nil means newKubeClient.
	newKube          func() (api.Client, error)
	unresolvedLogged bool
	fin              *reaper.Finisher
	rp               *reaper.Reaper
	lim              *limits.Book
	bgt              *budget.Gate
	// snap takes snapshots for the command center (#413); nothing renders it yet.
	snap *snapshot.Taker
	// budgetNow is the budget gate's clock; nil means time.Now (tests set it).
	budgetNow func() time.Time

	mu      sync.Mutex
	closers []func(context.Context)
}

// onClose registers fn to run when serve returns (committer worktrees, for
// example). Closers run in reverse order.
func (w *wiring) onClose(fn func(context.Context)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closers = append(w.closers, fn)
}

func (w *wiring) closeAll() {
	w.mu.Lock()
	cs := w.closers
	w.closers = nil
	w.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i](ctx)
	}
}

// ardRoot is the executor root, ~/.ard. The executor and the redactor's path
// rules both use it, so they cannot disagree.
func (w *wiring) ardRoot() string { return filepath.Join(w.Home, ".ard") }

// hostPaths are the controller host paths close-out replaces in committed text.
func (w *wiring) hostPaths() closeout.HostPaths {
	return closeout.HostPaths{Home: w.Home, ArdRoot: w.ardRoot(), TempDir: os.TempDir()}
}

// EnvAgentImage is the host setting naming the default agent image.
const EnvAgentImage = "ARD_AGENT_IMAGE"

// jobExecutor is the executor every loop shares: *local.Local or *kube.Executor.
type jobExecutor interface {
	executor.Executor
	Prune(ctx context.Context, olderThan time.Duration) error
}

// errModeUnknown means the executor mode is not known yet: no config has
// loaded and the cluster flags are set, so it may be k8s.
var errModeUnknown = errors.New("executor mode unknown: no config has loaded yet")

// newKubeClient is the in-cluster client. A variable so tests inject a fake.
var newKubeClient = func() (api.Client, error) { return api.InCluster(api.RESTOptions{}) }

func (w *wiring) k8sFlagsSet() bool {
	k := w.K8s
	return k.Namespace != "" || k.Instance != "" || k.AgentImage != "" || k.PodImage != ""
}

// normMode maps a config's executor to "local" or "k8s"; a Resolved built by
// hand in a test may leave it empty.
func normMode(m string) string {
	if m == "" {
		return "local"
	}
	return m
}

// checkExecutor refuses a config whose executor differs from the one this
// process started with: the mode is fixed until restart, so serve pauses. A
// k8s config that arrives before any executor exists builds it here, so a
// build error (a PAT) pauses serve with that message.
func (w *wiring) checkExecutor(c *config.Resolved) error {
	w.initMu.Lock()
	mode := w.mode
	w.initMu.Unlock()
	if mode != "" && normMode(c.Executor) != mode {
		return fmt.Errorf("executor changed from %s to %s; restart ard serve", mode, normMode(c.Executor))
	}
	if mode == "" && normMode(c.Executor) == "k8s" {
		w.cfg.Store(c)
		if _, err := w.newExecutor(); err != nil {
			return err
		}
	}
	return nil
}

// newExecutor builds the executor every loop uses; its host-mode hook
// appends a state.EventHostUntrusted row to the controller database. Every
// loop that runs jobs (scanners, and later triage, workers and reviewers)
// must build its executor here, so host-mode warnings are recorded.
//
// There is one executor per process: it tracks the jobs it started, and the
// Reaper must ask the same instance that the loops used. A build failure is
// not cached. The mode comes from the first loaded config (executor: local or
// k8s) and never changes. Before any config loads the mode is local, unless
// the cluster flags are set: then it is errModeUnknown.
func (w *wiring) newExecutor() (jobExecutor, error) {
	w.initMu.Lock()
	defer w.initMu.Unlock()
	if w.exec != nil {
		return w.exec, nil
	}
	mode := w.mode
	if mode == "" {
		if c := w.cfg.Load(); c != nil {
			mode = normMode(c.Executor)
		} else if w.k8sFlagsSet() {
			if !w.unresolvedLogged {
				w.unresolvedLogged = true
				w.Log.Error("serve.executor_unresolved", "hint", "no config loaded; job loops stay off until restart")
			}
			return nil, errModeUnknown
		} else {
			mode = "local"
		}
	}
	var ex jobExecutor
	var err error
	switch mode {
	case "local":
		ex, err = local.New(local.Options{
			Root: w.ardRoot(), AgentImage: w.AgentImage, Log: w.Log, SidecarToken: w.Token, CloneDir: w.Root,
			OnHostUntrusted: recordHostUntrusted(w.Store),
		})
	case "k8s":
		ex, err = w.newKubeExecutor()
	default:
		err = fmt.Errorf("unknown executor %q", mode)
	}
	if err != nil {
		return nil, err
	}
	w.exec, w.mode = ex, mode
	return ex, nil
}

// newKubeExecutor builds the Kubernetes executor from the --k8s-* settings.
func (w *wiring) newKubeExecutor() (jobExecutor, error) {
	minter, err := github.ScopedMinter(w.Auth)
	if err != nil {
		return nil, fmt.Errorf("executor k8s needs a GitHub App credential: %w", err)
	}
	k := w.K8s
	for _, m := range []struct{ flag, v string }{
		{"--k8s-namespace", k.Namespace}, {"--k8s-instance", k.Instance},
		{"--k8s-agent-image", k.AgentImage}, {"--k8s-pod-image", k.PodImage},
	} {
		if m.v == "" {
			return nil, fmt.Errorf("executor k8s: %s is required", m.flag)
		}
	}
	for _, im := range []string{k.AgentImage, k.PodImage} {
		if err := executor.CheckImageRef(im); err != nil {
			return nil, err
		}
	}
	res, err := parseResources(k.Resources)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.New(workspace.Options{Root: w.ardRoot(), Git: local.ExecGit(""), SidecarToken: w.Token,
		CloneDir: w.Root, Log: w.Log})
	if err != nil {
		return nil, err
	}
	mk := w.newKube
	if mk == nil {
		mk = newKubeClient
	}
	client, err := mk()
	if err != nil {
		return nil, fmt.Errorf("executor k8s: %w", err)
	}
	return kube.New(kube.Options{
		Client: client, Tokens: minter, WS: ws, Namespace: k.Namespace, Instance: k.Instance,
		Repo: w.Repo.String(), AgentImage: k.AgentImage, PodImage: k.PodImage, GitBase: k.GitBase,
		Resources: res, PreBudget: k.PreBudget, ResultGrace: k.ResultGrace,
		Policy: executor.EnvPolicy{
			AllowEnv:   map[string]bool{"ANTHROPIC_API_KEY": true, "CLAUDE_CODE_OAUTH_TOKEN": true},
			AllowFixed: map[string]bool{"CLAUDE_CONFIG_DIR": true, "DISABLE_AUTOUPDATER": true, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": true, "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS": true},
		},
		ConfigDirEnv: "CLAUDE_CONFIG_DIR", Log: w.Log,
	})
}

// parseResources applies --k8s-resources to the defaults. Keys: cpu and
// memory set the agent's requests, memory-limit its memory limit and
// ephemeral-storage its ephemeral-storage limit.
func parseResources(s string) (kube.Resources, error) {
	r := kube.DefaultResources()
	if strings.TrimSpace(s) == "" {
		return r, nil
	}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok || v == "" {
			return r, fmt.Errorf("--k8s-resources: %q is not name=quantity", kv)
		}
		switch k {
		case "cpu", "memory":
			r.Agent.Requests[k] = v
		case "memory-limit":
			r.Agent.Limits["memory"] = v
		case "ephemeral-storage":
			r.Agent.Limits[k] = v
		default:
			return r, fmt.Errorf("--k8s-resources: unknown name %q (cpu, memory, memory-limit, ephemeral-storage)", k)
		}
	}
	return r, nil
}

// environ is the process environment plus the executor's live job tokens
// (ARD_K8S_TOKEN_<n>), so they are masked in close-out text and logs like any
// other secret. The name matches the redactor's secret-name rule.
func (w *wiring) environ() []string {
	env := os.Environ()
	w.initMu.Lock()
	ex := w.exec
	w.initMu.Unlock()
	if sx, ok := ex.(interface{ Secrets() []string }); ok {
		for i, v := range sx.Secrets() {
			env = append(env, fmt.Sprintf("ARD_K8S_TOKEN_%d=%s", i, v))
		}
	}
	return env
}

// recordHostUntrusted returns the executor hook that appends a
// state.EventHostUntrusted row for a host-mode job.
func recordHostUntrusted(store *state.Store) func(context.Context, executor.Handle) error {
	return func(ctx context.Context, h executor.Handle) error {
		return store.AppendEvent(ctx, state.Event{
			Kind: state.EventHostUntrusted, JobID: h.JobID, Issue: h.Issue, Stage: h.Stage, Role: h.Role,
		})
	}
}

// Pool returns the slot pool adopted at startup. It panics if called before
// the pool-adopt hook has run, which means a loop used it in its constructor.
func (w *wiring) Pool() *pool.Pool {
	p := w.pool.Load()
	if p == nil {
		panic("pool used before pool-adopt")
	}
	return p
}

// registerLoops returns the loops to run: the scanners loop (#23), the stale
// closer (#25), the triage loop (#29), the worker loop (#33), the reviewer loop
// (#35) and the Reaper (#37) the costs loop (#172) and the budget notice loop (#237) so far. Loops that run jobs build their executor
// with w.newExecutor(), end jobs through w.finisher() and share w.closer(). The
// merger loop adds one constructor call here. It is a variable so tests can observe wiring.
var registerLoops = func(w *wiring) []controller.Loop {
	var loops []controller.Loop
	if l := scannerLoop(w); l != nil {
		loops = append(loops, l)
	}
	loops = append(loops, stale.New(w.Reader, w.Effects, nil))
	if l := triageLoop(w); l != nil {
		loops = append(loops, l)
	}
	if l := workerLoop(w); l != nil {
		loops = append(loops, l)
	}
	if l := reviewerLoop(w); l != nil {
		loops = append(loops, l)
	}
	loops = append(loops, reviewfixLoop(w), mergerLoop(w), prwatchLoop(w), janitorLoop(w))
	if l := reaperLoop(w); l != nil {
		loops = append(loops, l)
	}
	loops = append(loops, costs.New(costs.Deps{Store: w.Store, Reader: w.Reader, Effects: w.Effects,
		Repo: w.Repo, Locks: &w.bodyLocks, Log: w.Log}))
	loops = append(loops, budgetLoop(w))
	loops = append(loops, digestLoop(w))
	loops = append(loops, &controller.ApprovalWatch{Search: func(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error) {
		return w.Reader.SearchIssues(ctx, q, o)
	}, Wake: w.wakeLoops, Paused: w.Paused})
	return loops
}

// resolveRepo parses --repo, or falls back to the origin remote of root.
// Errors never include the remote URL, which may embed a token.
func resolveRepo(ctx context.Context, flag, root string) (github.Repo, error) {
	if flag != "" {
		r, err := github.ParseRepo(flag)
		if err != nil {
			return github.Repo{}, fmt.Errorf("ard serve: %w", err)
		}
		return r, nil
	}
	url, err := gitx.New("").RemoteURL(ctx, root, "origin")
	if err != nil {
		return github.Repo{}, errors.New("ard serve: --repo owner/repo is required (no origin remote to fall back on)")
	}
	r, err := github.RepoFromRemote(url)
	if err != nil {
		return github.Repo{}, errors.New("ard serve: --repo owner/repo is required (origin remote is not a github.com owner/repo URL)")
	}
	return r, nil
}

// forceOnSecondSignal returns a channel closed on the first signal received
// after ctx is done. cli.Run's NotifyContext consumes the first signal and
// keeps catching later ones, so without this a second Ctrl-C does nothing.
func forceOnSecondSignal(ctx context.Context) <-chan struct{} {
	force := make(chan struct{})
	go func() {
		<-ctx.Done()
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(ch)
		<-ch
		close(force)
	}()
	return force
}

// buildDeps is the production builder: credentials from the environment,
// the client and effect layer from effects.Open. Dev-token auth is refused
// outside dry-run.
func buildDeps(ctx context.Context, e *cli.Env, repo github.Repo, stateDir string) (deps, error) {
	auth, err := github.AuthFromEnv(os.Getenv, os.ReadFile)
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	if auth.Kind() == github.AuthToken && !e.DryRun {
		return deps{}, errors.New("ard serve: ARD_GITHUB_TOKEN auth is for --dry-run only; configure a GitHub App for live runs")
	}
	cred, err := modelauth.Resolve(modelauth.Mode(os.Getenv(modelauth.EnvMode)), claude.New(claude.Options{}).CredentialEnv, os.Getenv)
	var model modelauth.Credential
	switch {
	case errors.Is(err, modelauth.ErrNoCredential):
		// Only agent roles need one; deterministic scanners run without.
		e.Log.Info("serve.model_credential_unset", "hint", "agent jobs will fail until one is set", "error", err)
	case err != nil:
		return deps{}, fmt.Errorf("ard serve: model credential: %w", err)
	default:
		model = cred
		e.Log.Info("serve.model_credential", "credential", cred)
	}
	red, err := redact.New(redact.FromEnviron(os.Environ()), redact.Options{})
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	o := effects.Options{
		Redact: func(text string) string { return redactText(context.WithoutCancel(ctx), e.Log, red, text) },
		DryRun: e.DryRun,
		GitHub: github.Config{Repo: repo, Auth: auth, Log: e.Log},
		Log:    e.Log,
	}
	if e.DryRun {
		o.LedgerPath = filepath.Join(stateDir, "effects.jsonl")
	}
	fx, rd, err := effects.Open(ctx, o)
	if err != nil {
		return deps{}, fmt.Errorf("ard serve: %w", err)
	}
	d := deps{Reader: rd, Effects: fx, Token: auth.Token, Auth: auth, RemoteURL: fmt.Sprintf("https://github.com/%s.git", repo), Model: model}
	if c, ok := fx.(interface{ Close() error }); ok {
		d.Close = c.Close
	}
	return d, nil
}

// redactText masks known secrets in text bound for the dry-run ledger and
// log. It fails closed: a scan error or hit replaces the whole text, and the
// reason is logged without the text itself.
func redactText(ctx context.Context, log *slog.Logger, rd *redact.Redactor, text string) string {
	res, err := rd.Text(ctx, []byte(text))
	switch {
	case err != nil:
		log.Warn("dryrun.redact_failed", "error", err)
		return "[REDACTED: secret scan failed]"
	case res.Flagged:
		log.Warn("dryrun.redact_flagged", "findings", len(res.Findings))
		return "[REDACTED: secret scan flagged this text]"
	}
	return string(res.Clean)
}
