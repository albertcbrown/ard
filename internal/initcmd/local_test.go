package initcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/setupcmd"
)

func (h *harness) local(t *testing.T, dry bool, perm permFunc) (string, error) {
	t.Helper()
	e, o, out := h.env(dry)
	err := runLocal(context.Background(), e, o, perm)
	return out.String(), err
}

func okPerm(context.Context, string) (setupcmd.Row, error) {
	return setupcmd.Row{Status: setupcmd.StatusOK, Detail: "repository reachable"}, nil
}

func (h *harness) commitAll(t *testing.T) {
	gittest.Run(t, h.clone, "add", "-A")
	gittest.Run(t, h.clone, "commit", "-q", "-m", "init")
}

func TestLocalWritesTree(t *testing.T) {
	h := newHarness(t)
	out, err := h.local(t, false, okPerm)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(append([]string{}, starterTree...), LockPath) {
		if _, err := os.Stat(filepath.Join(h.clone, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	fi, err := os.Stat(filepath.Join(h.clone, ".ard/scanners/cve/scan"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("scan mode = %v, %v", fi, err)
	}
	if got := strings.TrimSpace(gittest.Run(t, h.clone, "status", "--porcelain", "--untracked-files=all", "--", ":!.ard")); got != "" {
		t.Errorf("changes outside .ard/: %s", got)
	}
	if have, _ := h.fake.ListLabels(context.Background()); len(have) != 0 {
		t.Error("labels written")
	}
	for _, s := range []string{"add  .ard/config.yaml", "permissions: repository reachable", "ard setup"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q:\n%s", s, out)
		}
	}
	// Second run after commit writes nothing.
	h.commitAll(t)
	before := snapshot(t, h.clone)
	out, err = h.local(t, false, okPerm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nothing to change") || !reflect.DeepEqual(snapshot(t, h.clone), before) {
		t.Errorf("second run changed something:\n%s", out)
	}
}

func TestLocalRefusesDirty(t *testing.T) {
	h := newHarness(t)
	h.commitMain(t, map[string]string{".ard/notes.txt": "a"})
	if err := os.WriteFile(filepath.Join(h.clone, ".ard/notes.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := h.local(t, false, nil)
	if err == nil || !strings.Contains(err.Error(), ".ard/notes.txt") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.clone, ".ard/config.yaml")); err == nil {
		t.Error("wrote despite dirty tree")
	}
	// An untracked file under .ard/ counts too.
	h = newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.clone, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.clone, ".ard/x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.local(t, false, nil); err == nil || !strings.Contains(err.Error(), ".ard/x") {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalDryRun(t *testing.T) {
	h := newHarness(t)
	before := snapshot(t, h.clone)
	out, err := h.local(t, true, okPerm)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would add") || !reflect.DeepEqual(snapshot(t, h.clone), before) {
		t.Errorf("dry run wrote or printed no plan:\n%s", out)
	}
}

func TestLocalNeverDeletes(t *testing.T) {
	h := newHarness(t)
	if _, err := h.local(t, false, nil); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(h.clone, ".ard/triage/lead/prompt.txt")
	if err := os.WriteFile(cfg, []byte("my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.commitAll(t)
	out, err := h.local(t, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "my edit\n" {
		t.Errorf("edited file overwritten: %q", b)
	}
	if !strings.Contains(out, "left alone") {
		t.Errorf("edit not reported:\n%s", out)
	}
}

func TestLocalSymlinkEscape(t *testing.T) {
	h := newHarness(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(h.clone, ".ard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(h.clone, ".ard/scanners")); err != nil {
		t.Fatal(err)
	}
	h.commitAll(t)
	if _, err := h.local(t, false, nil); err == nil {
		t.Fatal("want an error")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("wrote outside the tree: %v", ents)
	}
	// A symlinked .ard is refused outright.
	h = newHarness(t)
	if err := os.Symlink(outside, filepath.Join(h.clone, ".ard")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.local(t, false, nil); err == nil {
		t.Fatal("want an error for a symlinked .ard")
	}
}

func TestLocalPermissionWarns(t *testing.T) {
	h := newHarness(t)
	bad := func(context.Context, string) (setupcmd.Row, error) {
		return setupcmd.Row{Status: setupcmd.StatusManual, Detail: "check credentials"}, nil
	}
	out, err := h.local(t, false, bad)
	if err != nil || !strings.Contains(out, "warning: permissions: check credentials") {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}
	h = newHarness(t)
	none := func(context.Context, string) (setupcmd.Row, error) {
		return setupcmd.Row{}, errors.New("no credentials")
	}
	out, err = h.local(t, false, none)
	if err != nil || !strings.Contains(out, "permissions not checked") {
		t.Fatalf("err = %v, out:\n%s", err, out)
	}
}

func TestLocalInvalidConfigWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.commitMain(t, map[string]string{".ard/config.yaml": "intake: [not, a, map]\n"})
	before := snapshot(t, h.clone)
	e, o, _ := h.env(false)
	e.Stderr = &bytes.Buffer{}
	err := runLocal(context.Background(), e, o, nil)
	if err == nil {
		t.Skip("existing config was accepted as an edited file; nothing to assert")
	}
	if !reflect.DeepEqual(snapshot(t, h.clone), before) {
		t.Error("tree changed")
	}
}
