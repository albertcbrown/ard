package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/wtlock"
)

// worktreeAdminTimeout bounds each worktree add, remove and prune that runs
// under the shared lock, so one hung git cannot stall every lane.
const worktreeAdminTimeout = 30 * time.Second

// worktreeLock takes the process-wide worktree-administration lock for the
// repository at repoDir, keyed by its common git directory.
func (g *Git) worktreeLock(ctx context.Context, repoDir string) (func(), error) {
	out, _, err := g.run(ctx, repoDir, nil, "", nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("gitx: worktree lock %s: %w", repoDir, err)
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoDir, dir)
	}
	unlock, err := wtlock.Lock(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("gitx: worktree lock %s: %w", repoDir, err)
	}
	return unlock, nil
}

// The functions in this file only touch the local repository or read from a
// remote. They are not effects: commit and push stay in write.go and are
// reachable only through internal/effects.

// ErrRefNotFound means a fetch named a remote ref that does not exist.
var ErrRefNotFound = errors.New("gitx: remote ref not found")

// ErrRepoAccess means a fetch got a 404 or 403 from the remote: the
// repository does not exist or the credential cannot read it. Retrying
// cannot help.
var ErrRepoAccess = errors.New("repository not found or access denied")

// Change is one entry of the staged diff.
type Change struct {
	Path    string
	NewMode string // "000000" for a deletion
	Status  byte   // A, M, D, T ...
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// Version returns the major and minor version of the git binary.
func (g *Git) Version(ctx context.Context) (major, minor int, err error) {
	out, _, err := g.run(ctx, "", nil, "", nil, "version")
	if err != nil {
		return 0, 0, fmt.Errorf("gitx: version: %w", err)
	}
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return 0, 0, fmt.Errorf("gitx: unparseable version %q", strings.TrimSpace(out))
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return major, minor, nil
}

// WorktreeAdd checks ref out detached at path. Detached, because the branch
// may already be checked out elsewhere and git refuses a second checkout.
// Serialised per repository with every other worktree add/remove in this
// process (internal/wtlock).
func (g *Git) WorktreeAdd(ctx context.Context, repoDir, path, ref string) error {
	ctx, cancel := context.WithTimeout(ctx, worktreeAdminTimeout)
	defer cancel()
	unlock, err := g.worktreeLock(ctx, repoDir)
	if err != nil {
		return err
	}
	defer unlock()
	if _, _, err := g.run(ctx, repoDir, nil, "", nil, "worktree", "add", "--detach", "--force", path, ref); err != nil {
		return fmt.Errorf("gitx: worktree add %s: %w", path, err)
	}
	return nil
}

// WorktreeRemove deletes the worktree at path, whatever state it is in, and
// prunes stale registrations. A missing worktree is not an error. It is
// cleanup, so it runs even when ctx is cancelled, bounded by a 30 s deadline.
// Serialised per repository with every other worktree add/remove in this
// process (internal/wtlock).
func (g *Git) WorktreeRemove(ctx context.Context, repoDir, path string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), worktreeAdminTimeout)
	defer cancel()
	unlock, err := g.worktreeLock(ctx, repoDir)
	if err != nil {
		return err
	}
	defer unlock()
	_, _, _ = g.run(ctx, repoDir, nil, "", nil, "worktree", "remove", "--force", path)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("gitx: remove %s: %w", path, err)
	}
	if _, _, err := g.run(ctx, repoDir, nil, "", nil, "worktree", "prune"); err != nil {
		return fmt.Errorf("gitx: worktree prune: %w", err)
	}
	return nil
}

// FetchRef fetches refspec from url into repoDir. token may be nil. A remote
// ref that does not exist returns ErrRefNotFound.
func (g *Git) FetchRef(ctx context.Context, repoDir, url, refspec string, token TokenFunc) error {
	return g.fetch(ctx, repoDir, url, refspec, token, false)
}

// FetchShallow is FetchRef with --depth=1 and -c gc.auto=0 (no detached
// gc --auto is left running, and cache hits stay deterministic). A remote ref
// that does not exist ("couldn't find remote ref") or a SHA the remote does
// not have ("not our ref") returns ErrRefNotFound.
func (g *Git) FetchShallow(ctx context.Context, repoDir, url, refspec string, token TokenFunc) error {
	return g.fetch(ctx, repoDir, url, refspec, token, true)
}

func (g *Git) fetch(ctx context.Context, repoDir, url, refspec string, token TokenFunc, shallow bool) error {
	var env, secrets []string
	if token != nil {
		tok, err := token(ctx)
		if err != nil {
			return fmt.Errorf("gitx: fetch token: %w", err)
		}
		env, secrets = pushEnv(tok, url), []string{tok}
	}
	args := []string{"fetch", "--no-tags", "--no-write-fetch-head", "--quiet"}
	if shallow {
		args = []string{"-c", "gc.auto=0", "fetch", "--depth=1", "--no-tags", "--no-write-fetch-head", "--quiet"}
	}
	_, stderr, err := g.run(ctx, repoDir, env, "", secrets, append(args, url, refspec)...)
	if err != nil {
		if cerr := classifyFetch(stderr, shallow); cerr != nil {
			return fmt.Errorf("gitx: fetch %s: %w", refspec, cerr)
		}
		return fmt.Errorf("gitx: fetch %s: %w", refspec, err)
	}
	return nil
}

// classifyFetch maps git's stderr to a typed cause, or nil when it is none
// of them.
func classifyFetch(stderr string, shallow bool) error {
	if shallow {
		for _, m := range []string{"Repository not found", "' not found", "returned error: 403", "returned error: 404"} {
			if strings.Contains(stderr, m) {
				return ErrRepoAccess
			}
		}
	}
	if strings.Contains(stderr, "couldn't find remote ref") || (shallow && strings.Contains(stderr, "not our ref")) {
		return ErrRefNotFound
	}
	return nil
}

// InitBare creates an empty bare repository at dir (git init --bare --quiet).
// An existing repository at dir is left as it is.
func (g *Git) InitBare(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("gitx: init bare %s: %w", dir, err)
	}
	if _, _, err := g.run(ctx, dir, nil, "", nil, "init", "--bare", "--quiet", "."); err != nil {
		return fmt.Errorf("gitx: init bare %s: %w", dir, err)
	}
	return nil
}

// ResetHard moves the worktree, index and HEAD to ref.
func (g *Git) ResetHard(ctx context.Context, dir, ref string) error {
	if _, _, err := g.run(ctx, dir, nil, "", nil, "reset", "--hard", "--quiet", ref); err != nil {
		return fmt.Errorf("gitx: reset %s: %w", ref, err)
	}
	return nil
}

// CleanAll removes untracked and ignored files and nested repositories.
func (g *Git) CleanAll(ctx context.Context, dir string) error {
	if _, _, err := g.run(ctx, dir, nil, "", nil, "clean", "-ffdxq"); err != nil {
		return fmt.Errorf("gitx: clean: %w", err)
	}
	return nil
}

// AddForce stages paths even when .gitignore matches them.
func (g *Git) AddForce(ctx context.Context, dir string, paths ...string) error {
	args := append([]string{"--literal-pathspecs", "add", "-f", "--"}, paths...)
	if _, _, err := g.run(ctx, dir, nil, "", nil, args...); err != nil {
		return fmt.Errorf("gitx: add: %w", err)
	}
	return nil
}

// RmCached stages the removal of paths; an untracked path is ignored.
func (g *Git) RmCached(ctx context.Context, dir string, paths ...string) error {
	args := append([]string{"--literal-pathspecs", "rm", "-q", "--cached", "--ignore-unmatch", "--"}, paths...)
	if _, _, err := g.run(ctx, dir, nil, "", nil, args...); err != nil {
		return fmt.Errorf("gitx: rm: %w", err)
	}
	return nil
}

// StagedChanges lists the staged diff with new modes, without rename
// detection.
func (g *Git) StagedChanges(ctx context.Context, dir string) ([]Change, error) {
	out, _, err := g.run(ctx, dir, nil, "", nil, "diff", "--cached", "--raw", "-z", "--no-renames")
	if err != nil {
		return nil, fmt.Errorf("gitx: staged changes: %w", err)
	}
	tok := strings.Split(out, "\x00")
	var cs []Change
	for i := 0; i+1 < len(tok); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(tok[i], ":")) // oldmode newmode oldsha newsha status
		if len(meta) < 5 {
			return nil, fmt.Errorf("gitx: staged changes: bad record %q", tok[i])
		}
		cs = append(cs, Change{Path: tok[i+1], NewMode: meta[1], Status: meta[4][0]})
	}
	return cs, nil
}

// ChangedPaths lists the paths among paths that differ between from and to.
func (g *Git) ChangedPaths(ctx context.Context, dir, from, to string, paths ...string) ([]string, error) {
	args := append([]string{"--literal-pathspecs", "diff", "--name-only", "-z", from, to, "--"}, paths...)
	out, _, err := g.run(ctx, dir, nil, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: changed paths: %w", err)
	}
	var ps []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
}

// PathChange is one path whose worktree content differs from a base commit.
type PathChange struct {
	Path    string // slash-separated, relative to the worktree root
	Deleted bool   // present at base, absent in the worktree
}

// ChangedSince lists the paths under pathspecs (literal, directory prefixes
// allowed) that differ between commit base and the working tree: tracked
// changes, deletions and untracked files that are not ignored. It is
// read-only and sees uncommitted work, which is all an agent leaves behind.
func (g *Git) ChangedSince(ctx context.Context, dir, base string, paths ...string) ([]PathChange, error) {
	args := append([]string{"--literal-pathspecs", "diff", "--name-status", "-z", "--no-renames", base, "--"}, paths...)
	out, _, err := g.run(ctx, dir, nil, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: changed since %s: %w", base, err)
	}
	var cs []PathChange
	tok := strings.Split(out, "\x00")
	for i := 0; i+1 < len(tok); i += 2 {
		if tok[i] == "" {
			continue
		}
		cs = append(cs, PathChange{Path: tok[i+1], Deleted: tok[i][0] == 'D'})
	}
	args = append([]string{"--literal-pathspecs", "ls-files", "--others", "--exclude-standard", "-z", "--"}, paths...)
	out, _, err = g.run(ctx, dir, nil, "", nil, args...)
	if err != nil {
		return nil, fmt.Errorf("gitx: untracked files: %w", err)
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			cs = append(cs, PathChange{Path: p})
		}
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Path < cs[j].Path })
	return cs, nil
}

// maxExportBytes caps the total size ExportPaths will write.
var maxExportBytes int64 = 64 << 20

// ExportPaths writes the files under paths at commit sha in repoDir into
// dest, which must not exist; it is created 0o755. Paths missing at sha are
// skipped (none present yields an empty dest). Regular files are written
// 0o644, or 0o755 when git records mode 100755; directories 0o755. A
// symlink (120000) or submodule (160000) under paths is an error, as is a
// total size over 64 MiB. On error dest is removed.
//
// It reads blobs straight from the object store rather than through git
// archive, which applies export-ignore and export-subst from .gitattributes
// and could drop or rewrite role files.
func (g *Git) ExportPaths(ctx context.Context, repoDir, sha, dest string, paths ...string) error {
	return g.exportPaths(ctx, repoDir, sha, dest, false, paths)
}

// ExportTree is ExportPaths for the whole tree at sha: the same symlink,
// submodule, mode and 64 MiB rules, no .git, dest must not exist. (Passing
// "." to ExportPaths does not work: no entry is "under" it.)
func (g *Git) ExportTree(ctx context.Context, repoDir, sha, dest string) error {
	return g.exportPaths(ctx, repoDir, sha, dest, true, nil)
}

func (g *Git) exportPaths(ctx context.Context, repoDir, sha, dest string, whole bool, paths []string) (err error) {
	if _, serr := os.Lstat(dest); serr == nil {
		return fmt.Errorf("gitx: export to %s: already exists", dest)
	} else if !errors.Is(serr, fs.ErrNotExist) {
		return fmt.Errorf("gitx: export to %s: %w", dest, serr)
	}
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), "..") {
			return fmt.Errorf("gitx: export: bad path %q", p)
		}
	}
	args := append([]string{"--literal-pathspecs", "ls-tree", "-r", "-l", "-z", "--full-tree", sha, "--"}, paths...)
	if whole {
		args = []string{"ls-tree", "-r", "-l", "-z", "--full-tree", sha}
	}
	out, _, err := g.run(ctx, repoDir, nil, "", nil, args...)
	if err != nil {
		return fmt.Errorf("gitx: export %.12s: %w", sha, err)
	}
	type entry struct {
		path, obj string
		exec      bool
	}
	var entries []entry
	var total int64
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			return fmt.Errorf("gitx: export %.12s: bad ls-tree record %q", sha, rec)
		}
		mode, typ, obj := f[0], f[1], f[2]
		if !whole && !underAny(path, paths) {
			return fmt.Errorf("gitx: export %.12s: %q is outside the requested paths", sha, path)
		}
		switch {
		case mode == "120000":
			return fmt.Errorf("gitx: export %.12s: %s is a symlink; symlinks are not allowed", sha, path)
		case mode == "160000" || typ == "commit":
			return fmt.Errorf("gitx: export %.12s: %s is a submodule; submodules are not allowed", sha, path)
		case typ != "blob" || (mode != "100644" && mode != "100755"):
			return fmt.Errorf("gitx: export %.12s: %s has unsupported mode %s", sha, path, mode)
		}
		size, perr := strconv.ParseInt(f[3], 10, 64)
		if perr != nil {
			return fmt.Errorf("gitx: export %.12s: bad size in %q", sha, rec)
		}
		if total += size; total > maxExportBytes {
			if whole {
				return fmt.Errorf("gitx: export %.12s: more than %d MiB in the tree", sha, maxExportBytes>>20)
			}
			return fmt.Errorf("gitx: export %.12s: more than %d MiB under %v", sha, maxExportBytes>>20, paths)
		}
		entries = append(entries, entry{path: path, obj: obj, exec: mode == "100755"})
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("gitx: export: %w", err)
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return fmt.Errorf("gitx: export: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dest)
		}
	}()
	if err := os.Chmod(dest, 0o755); err != nil { // not subject to the umask
		return fmt.Errorf("gitx: export: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	var in strings.Builder
	for _, e := range entries {
		in.WriteString(e.obj + "\n")
	}
	blobs, _, err := g.run(ctx, repoDir, nil, in.String(), nil, "cat-file", "--batch")
	if err != nil {
		return fmt.Errorf("gitx: export %.12s: read blobs: %w", sha, err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return fmt.Errorf("gitx: export: %w", err)
	}
	defer root.Close()
	for _, e := range entries {
		hdr, rest, ok := strings.Cut(blobs, "\n")
		hf := strings.Fields(hdr)
		if !ok || len(hf) != 3 || hf[0] != e.obj || hf[1] != "blob" {
			return fmt.Errorf("gitx: export %.12s: unexpected cat-file header %q for %s", sha, hdr, e.path)
		}
		n, perr := strconv.Atoi(hf[2])
		if perr != nil || n < 0 || len(rest) < n+1 {
			return fmt.Errorf("gitx: export %.12s: truncated cat-file output for %s", sha, e.path)
		}
		content := rest[:n]
		blobs = rest[n+1:]
		if err := writeExported(root, e.path, content, e.exec); err != nil {
			return fmt.Errorf("gitx: export %s: %w", e.path, err)
		}
	}
	return nil
}

func writeExported(root *os.Root, rel, content string, exec bool) error {
	if dir := path.Dir(rel); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// underAny reports whether p equals one of paths or lies beneath it.
func underAny(p string, paths []string) bool {
	for _, q := range paths {
		q = strings.TrimSuffix(q, "/")
		if p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}

// SetExec marks paths in dir's index as mode 100755 (git update-index
// --chmod=+x), whatever core.fileMode says. The paths must already be staged.
func (g *Git) SetExec(ctx context.Context, dir string, paths ...string) error {
	args := append([]string{"--literal-pathspecs", "update-index", "--chmod=+x", "--"}, paths...)
	if _, _, err := g.run(ctx, dir, nil, "", nil, args...); err != nil {
		return fmt.Errorf("gitx: set exec: %w", err)
	}
	return nil
}
