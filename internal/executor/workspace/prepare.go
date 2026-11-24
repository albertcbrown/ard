package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/executor"
)

// Prepare creates the worktree, writes inputs, makes the home and config dir,
// exports the sidecar and creates the log files. It returns a host Handle
// (Kind "host"); a caller that runs Docker adjusts it. On error it removes
// everything it made.
func (w *Workspace) Prepare(ctx context.Context, j executor.Job) (h executor.Handle, err error) {
	if err := j.Validate(); err != nil {
		return h, err
	}
	wt, err := w.WorktreePath(j.Repo, j.ID)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	if _, err := os.Lstat(wt); err == nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, executor.ErrExists)
	}
	sha, err := w.git.RevParse(ctx, j.RepoDir, j.Ref)
	if err != nil {
		return h, fmt.Errorf("resolve ref %q: %w: %w", j.Ref, executor.ErrClone, err)
	}
	now := w.now().UTC()
	base := LogBase(j.Stage, j.Role, now)
	logDir, err := w.LogDir(j.Repo, j.Issue)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	home, err := w.HomePath(j.Repo, j.ID)
	if err != nil {
		return h, fmt.Errorf("prepare %s: %w", j.ID, err)
	}
	h = executor.Handle{
		JobID: j.ID, Kind: "host", Issue: j.Issue, Stage: j.Stage, Role: j.Role,
		Worktree: wt, Home: home, RepoDir: j.RepoDir, BaseSHA: sha,
		LogPath: filepath.Join(logDir, base+".log"), StdoutPath: filepath.Join(logDir, base+".stdout"),
		View:    executor.Paths{Work: wt, Role: j.RoleDir},
		RoleDir: j.RoleDir, ReadOnly: j.ReadOnly, Network: j.Network,
		TrustedInput: j.TrustedInput, PreparedAt: now, Timeout: j.Timeout,
	}
	var created []string // paths to remove on failure
	worktreeAdded := false
	defer func() {
		if err == nil {
			return
		}
		if worktreeAdded {
			ChmodWritable(wt)
			if rerr := w.git.WorktreeRemove(context.WithoutCancel(ctx), j.RepoDir, wt, true); rerr != nil {
				err = errors.Join(err, rerr)
			}
			RemoveAllWritable(wt)
			w.git.WorktreePrune(context.WithoutCancel(ctx), j.RepoDir)
		}
		for _, p := range created {
			RemoveAllWritable(p) // a sidecar tree under home is a-w
		}
		h = executor.Handle{}
	}()

	if err = os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return h, fmt.Errorf("create work dir: %w", err)
	}
	if err = w.git.WorktreeAdd(ctx, j.RepoDir, wt, sha); err != nil {
		return h, fmt.Errorf("add worktree: %w: %w", executor.ErrClone, err)
	}
	worktreeAdded = true
	// The config dir lives inside home, so Remove and this function's
	// failure path remove it with home.
	created = append(created, home)
	var pop Populated
	if pop, err = Populate(PopulateSpec{Tree: wt, Home: home, Scrub: j.Scrub, Inputs: j.Inputs}); err != nil {
		return h, err
	}
	h.Scrubbed = pop.Scrubbed
	h.View.ConfigDir = pop.ConfigDir
	if j.History {
		// Host mode hands over the live clone: read-only is enforced only by the
		// Docker mount (:ro), not on the host.
		h.HistoryDir, h.View.History = j.RepoDir, j.RepoDir
	}
	// --- sidecar (#28): the executor clones, pins and mounts it read-only ---
	if j.Sidecar != (executor.Sidecar{}) {
		dest := filepath.Join(home, sidecarDirName)
		var scSHA string
		var fetched bool
		if scSHA, fetched, err = w.materialise(ctx, j.Repo, j.Sidecar, dest); err != nil {
			return h, err
		}
		h.SidecarDir, h.Sidecar, h.SidecarSHA = dest, j.Sidecar, scSHA
		h.View.Sidecar = dest
		w.log.Info("executor.sidecar_ready", "job", j.ID, "repo", j.Sidecar.Repo, "ref", j.Sidecar.Ref, "sha", scSHA, "fetched", fetched)
	}
	// --- end sidecar ---
	if err = os.MkdirAll(logDir, 0o700); err != nil {
		return h, fmt.Errorf("create log dir: %w", err)
	}
	for _, p := range []string{h.LogPath, h.StdoutPath} {
		var f *os.File
		if f, err = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err != nil {
			return h, fmt.Errorf("create log %s: %w", p, err)
		}
		f.Close()
		created = append(created, p)
	}
	if h.SidecarDir != "" { // #28: read-only in both modes; Docker also mounts :ro
		if err = ChmodReadOnly(h.SidecarDir); err != nil {
			return h, fmt.Errorf("make sidecar read-only: %w", err)
		}
	}
	return h, nil
}

// Discard removes everything Prepare made for h, log files included. A caller
// whose own steps after a successful Prepare fail uses it.
func (w *Workspace) Discard(ctx context.Context, h executor.Handle) error {
	err := w.Remove(ctx, h)
	for _, p := range []string{h.LogPath, h.StdoutPath} {
		if p != "" {
			os.Remove(p)
		}
	}
	return err
}
