package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
)

// imageTag is a content-addressed tag: it changes when any file or mode in
// the role directory changes.
func imageTag(stage, role, roleDir string) (string, error) {
	var files []string
	err := filepath.WalkDir(roleDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, _ := filepath.Rel(roleDir, p)
		st, err := os.Stat(p)
		if err != nil {
			return "", err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", filepath.ToSlash(rel), st.Mode().Perm(), len(b))
		h.Write(b)
	}
	return fmt.Sprintf("ard-role-%s-%s:%s", stage, role, hex.EncodeToString(h.Sum(nil))[:12]), nil
}

// runArgv builds the `docker run` argv. Secret values never appear: entries
// in modelEnv are passed by name and the value travels in the CLI's env.
func (l *Local) runArgv(h executor.Handle, cmd executor.Command, stdin bool) []string {
	a := []string{"run", "--rm", "--name", h.Container, "--init",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp",
		"--label", "ard.job=" + h.JobID,
		"--label", "ard.issue=" + strconv.Itoa(h.Issue),
		"--label", "ard.stage=" + h.Stage,
		"--label", "ard.role=" + h.Role,
	}
	work := h.Worktree + ":/work"
	if h.ReadOnly {
		work += ":ro"
	}
	a = append(a, "-v", work, "-v", h.RoleDir+":/role:ro")
	if h.SidecarDir != "" {
		a = append(a, "-v", h.SidecarDir+":/sidecar:ro")
	}
	a = append(a, "-w", "/work", "-e", "HOME=/tmp", "-e", "LANG=C.UTF-8", "-e", "GIT_TERMINAL_PROMPT=0")
	for _, kv := range ardEnv(h) {
		a = append(a, "-e", kv)
	}
	for _, kv := range cmd.Fixed {
		a = append(a, "-e", kv)
	}
	for _, kv := range cmd.Env {
		name, _, _ := strings.Cut(kv, "=")
		a = append(a, "-e", name)
	}
	if stdin {
		a = append(a, "-i")
	}
	a = append(a, h.Image, cmd.Path)
	return append(a, cmd.Args...)
}

func (l *Local) docker(ctx context.Context, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, l.dockerBin, args...)
	c.Env = os.Environ()
	return c.CombinedOutput()
}

// ensureImage builds the role image unless the tag already exists. Build
// output is appended to logPath.
func (l *Local) ensureImage(ctx context.Context, j executor.Job, logPath string) (string, error) {
	tag, err := imageTag(j.Stage, j.Role, j.RoleDir)
	if err != nil {
		return "", fmt.Errorf("hash role dir: %w", err)
	}
	if _, err := l.docker(ctx, "image", "inspect", tag); err == nil {
		return tag, nil
	}
	bctx, cancel := context.WithTimeout(ctx, l.buildTimeout)
	defer cancel()
	out, err := l.docker(bctx, "build", "-t", tag, "-f", j.Dockerfile, j.RoleDir)
	if f, ferr := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600); ferr == nil {
		f.Write(out)
		f.Close()
	}
	if err != nil {
		return "", fmt.Errorf("docker build %s: %w", tag, err)
	}
	return tag, nil
}

func (l *Local) dockerKill(sig string, h executor.Handle) {
	ctx, cancel := context.WithTimeout(context.Background(), 20e9)
	defer cancel()
	l.docker(ctx, "kill", "--signal", sig, h.Container)
}

func (l *Local) dockerRunning(ctx context.Context, h executor.Handle) bool {
	out, err := l.docker(ctx, "inspect", "-f", "{{.State.Running}}", h.Container)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}
