// Package jobpre is `ard job-pre`, the init step of a Kubernetes job pod. It
// reads the manifest and the git token from the per-job Secret, fetches the
// job's trees and lays them out for the agent. The token never reaches argv
// or the log, and never leaves this container.
package jobpre

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/executor/workspace"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// pathSet holds every location the step touches, so tests can redirect them.
type pathSet struct {
	secretDir, ctl, work, job, bin, agentBin, termLog string
	role, sidecar, history                            string
}

var paths = pathSet{
	secretDir: jobpod.PreSecretDir,
	ctl:       jobpod.MountCtl,
	work:      jobpod.MountWork,
	job:       jobpod.MountJob,
	bin:       jobpod.MountBin,
	agentBin:  jobpod.AgentBin,
	termLog:   "/dev/termination-log",
	role:      jobpod.JobRole,
	sidecar:   jobpod.JobSidecar,
	history:   jobpod.JobHistory,
}

// permanent marks an error a retry cannot fix.
type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

func perm(format string, a ...any) error { return permanent{fmt.Errorf(format, a...)} }

// Run executes the step. Permanent failures exit jobpod.ExitPermanent with a
// reason in the termination log; everything else exits jobpod.ExitTransient.
func Run(ctx context.Context, e *cli.Env, o cli.JobPreOpts) error {
	if !o.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, o.Deadline)
		defer cancel()
	}
	err := run(ctx, o)
	if err == nil {
		return nil
	}
	var p permanent
	if errors.Is(err, gitx.ErrRefNotFound) || errors.Is(err, gitx.ErrRepoAccess) || errors.As(err, &p) {
		_ = os.WriteFile(paths.termLog, []byte(truncate(err.Error(), 2048)), 0o644)
		return &cli.ExitError{Code: jobpod.ExitPermanent, Err: err}
	}
	return &cli.ExitError{Code: jobpod.ExitTransient, Err: err}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func readSecret(name string, limit int64) ([]byte, error) {
	f, err := os.Open(filepath.Join(paths.secretDir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return b, nil
}

func run(ctx context.Context, o cli.JobPreOpts) error {
	raw, err := readSecret(jobpod.ItemManifest, jobpod.MaxManifestBytes)
	if err != nil {
		return perm("manifest: %v", err)
	}
	m, err := jobpod.Decode(raw)
	if err != nil {
		return perm("%v", err)
	}
	tokB, err := readSecret(jobpod.ItemGitAccess, 4096)
	if err != nil {
		return perm("git token: %v", err)
	}
	token := strings.TrimSpace(string(tokB))
	tok := func(context.Context) (string, error) { return token, nil }
	if m.Role.Repo != m.Repo {
		return perm("role repo %q is not the job repo %q", m.Role.Repo, m.Repo)
	}

	syscallUmask(0o002)
	home := filepath.Join(paths.ctl, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	for k, v := range map[string]string{"HOME": home, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null"} {
		os.Setenv(k, v)
	}

	base := strings.TrimRight(o.GitBase, "/")
	urlOf := func(repo string) string { return base + "/" + repo + ".git" }
	g := gitx.New("")
	repoDir := filepath.Join(paths.ctl, "repo.git")
	if err := g.InitBare(ctx, repoDir); err != nil {
		return err
	}
	fetch := func(sha, ref string) error {
		if err := g.FetchShallow(ctx, repoDir, urlOf(m.Repo), "+"+sha+":"+ref, tok); err != nil {
			return err
		}
		got, err := g.RevParse(ctx, repoDir, sha)
		if err != nil {
			return err
		}
		if got != sha {
			return perm("fetched %s, want %s", got, sha)
		}
		return nil
	}
	if err := fetch(m.BaseSHA, "refs/ard/base"); err != nil {
		return err
	}
	if m.Role.SHA != m.BaseSHA {
		if err := fetch(m.Role.SHA, "refs/ard/role"); err != nil {
			return err
		}
	}
	if m.History {
		if err := g.InitBare(ctx, paths.history); err != nil {
			return err
		}
		if err := g.FetchRef(ctx, paths.history, urlOf(m.Repo), "+refs/heads/*:refs/heads/*", tok); err != nil {
			return err
		}
	}

	if err := g.CheckoutTree(ctx, repoDir, paths.work, m.BaseSHA); err != nil {
		return err
	}

	roleX := filepath.Join(paths.ctl, "role-x")
	if err := g.ExportPaths(ctx, repoDir, m.Role.SHA, roleX, m.Role.Path); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(roleX, filepath.FromSlash(m.Role.Path))); err != nil {
		return perm("role path %q not found at %s", m.Role.Path, m.Role.SHA)
	}
	if err := os.Rename(filepath.Join(roleX, filepath.FromSlash(m.Role.Path)), paths.role); err != nil {
		return err
	}
	if m.Sidecar != nil {
		scDir := filepath.Join(paths.ctl, "sidecar.git")
		if err := workspace.FetchPinned(ctx, g, scDir, urlOf(m.Sidecar.Repo), m.Sidecar.SHA, tok); err != nil {
			return err
		}
		if err := g.ExportTree(ctx, scDir, m.Sidecar.SHA, paths.sidecar); err != nil {
			return err
		}
	}

	if _, err := workspace.Populate(workspace.PopulateSpec{Tree: paths.work, Scrub: m.Scrub, Inputs: m.Inputs}); err != nil {
		return perm("populate: %v", err)
	}

	for _, d := range []string{paths.work, paths.history} {
		if err := groupWritable(d); err != nil {
			return err
		}
	}
	if err := groupWritableDir(paths.job); err != nil {
		return err
	}
	toLock := []string{paths.role, paths.sidecar}
	if m.ReadOnly {
		toLock = append(toLock, paths.work)
	}
	for _, d := range toLock {
		if _, err := os.Lstat(d); err != nil {
			continue
		}
		if err := workspace.ChmodReadOnly(d); err != nil {
			return err
		}
	}
	return installBinary()
}

// groupWritableDir gives dir g+rwxs. It is a no-op for a missing dir.
func groupWritableDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return nil
	}
	return os.Chmod(dir, st.Mode().Perm()|0o070|fs.ModeSetgid)
}

// groupWritable gives every directory under root g+rwxs and every file g+rw,
// so the agent, another user in the same group, can edit the tree.
func groupWritable(root string) error {
	if _, err := os.Lstat(root); err != nil {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, st.Mode().Perm()|0o070|fs.ModeSetgid)
		}
		if st.Mode().IsRegular() {
			return os.Chmod(p, st.Mode().Perm()|0o060)
		}
		return nil
	})
}

// installBinary copies this executable to the shared bin volume.
func installBinary() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	src, err := os.Open(self)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(paths.bin, ".ard-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), paths.agentBin)
}
