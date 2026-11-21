package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLegacy(t *testing.T) {
	tests := []struct {
		name  string
		repo  string
		files []string // relative to root; trailing "/" makes an empty dir
		want  bool
	}{
		{"empty root", "o/r", nil, false},
		{"dryrun only, clean", "o/r", []string{"state-dryrun/o/r/effects.jsonl"}, false},
		{"flat live", "o/r", []string{"state/o-r/_controller.db"}, true},
		{"flat dryrun", "o/r", []string{"state-dryrun/o-r/effects.jsonl"}, true},
		{"flat mixed case", "o/r", []string{"state/O-R/_controller.db"}, true},
		{"new layout no false positive", "a/b", []string{"state/a-b/c/_controller.db"}, false},
		{"empty legacy dir", "o/r", []string{"state/o-r/"}, false},
		{"legacy name is a file", "o/r", []string{"state/o-r"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, f := range tc.files {
				p := filepath.Join(root, f)
				if strings.HasSuffix(f, "/") {
					if err := os.MkdirAll(p, 0o700); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckLegacy(root, tc.repo)
			if got := errors.Is(err, ErrLegacyDir); got != tc.want || (!tc.want && err != nil) {
				t.Fatalf("err = %v, want legacy=%v", err, tc.want)
			}
			if tc.want {
				owner, name, _ := strings.Cut(tc.repo, "/")
				if !strings.Contains(err.Error(), filepath.Join(root, "state")) || !strings.Contains(err.Error(), owner+string(filepath.Separator)+name) {
					t.Errorf("message should name old and new paths: %v", err)
				}
			}
		})
	}
}

func TestCheckLegacyUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	root := t.TempDir()
	d := filepath.Join(root, "state")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(d, 0o700)
	err := CheckLegacy(root, "o/r")
	if err == nil || errors.Is(err, ErrLegacyDir) {
		t.Fatalf("err = %v, want a read error", err)
	}
}
