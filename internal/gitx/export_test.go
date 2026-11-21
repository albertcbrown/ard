package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

// commitFiles writes files (rel path -> mode+content) into clone and commits.
type fileSpec struct {
	content string
	mode    os.FileMode
}

func commitFiles(t *testing.T, clone string, files map[string]fileSpec) string {
	t.Helper()
	for rel, f := range files {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.content), f.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, f.mode); err != nil {
			t.Fatal(err)
		}
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "files")
	sha, err := New("").RevParse(context.Background(), clone, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func TestExportPaths(t *testing.T) {
	ctx := context.Background()
	g := New("")
	tests := []struct {
		name    string
		files   map[string]fileSpec
		symlink bool
		paths   []string
		want    map[string]os.FileMode // rel path -> mode; content is checked as "c:"+path
		wantErr bool
	}{
		{
			name: "regular, executable and nested files",
			files: map[string]fileSpec{
				".ard/config.yaml":          {"c:.ard/config.yaml", 0o644},
				".ard/scanners/cve/scan":    {"c:.ard/scanners/cve/scan", 0o755},
				".ard/scanners/cve/lib/a.x": {"c:.ard/scanners/cve/lib/a.x", 0o644},
				".ard/7/log.md":             {"c:.ard/7/log.md", 0o644},
				"README.md":                 {"c:README.md", 0o644},
			},
			paths: []string{".ard/config.yaml", ".ard/scanners"},
			want: map[string]os.FileMode{
				".ard/config.yaml":          0o644,
				".ard/scanners/cve/scan":    0o755,
				".ard/scanners/cve/lib/a.x": 0o644,
			},
		},
		{
			name:  "missing paths are skipped",
			files: map[string]fileSpec{".ard/triage/t/prompt.txt": {"c:.ard/triage/t/prompt.txt", 0o644}},
			paths: []string{".ard/config.yaml", ".ard/scanners", ".ard/triage"},
			want:  map[string]os.FileMode{".ard/triage/t/prompt.txt": 0o644},
		},
		{
			name:  "no path present gives an empty dest",
			files: map[string]fileSpec{"README.md": {"c:README.md", 0o644}},
			paths: []string{".ard/config.yaml", ".ard/scanners"},
			want:  map[string]os.FileMode{},
		},
		{
			name:    "symlink is an error",
			files:   map[string]fileSpec{".ard/scanners/x/scan": {"c:.ard/scanners/x/scan", 0o755}},
			symlink: true,
			paths:   []string{".ard/scanners"},
			wantErr: true,
		},
		{
			name:    "path with dotdot is an error",
			files:   map[string]fileSpec{"a": {"c:a", 0o644}},
			paths:   []string{".ard/../a"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, clone := gittest.NewRemote(t)
			if tc.symlink {
				if err := os.MkdirAll(filepath.Join(clone, ".ard/scanners/x"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/etc/passwd", filepath.Join(clone, ".ard/scanners/x/link")); err != nil {
					t.Fatal(err)
				}
			}
			sha := commitFiles(t, clone, tc.files)
			dest := filepath.Join(t.TempDir(), "out", "snap")
			err := g.ExportPaths(ctx, clone, sha, dest, tc.paths...)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				if _, serr := os.Lstat(dest); !os.IsNotExist(serr) {
					t.Errorf("dest left behind after error: %v", serr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]os.FileMode{}
			err = filepath.WalkDir(dest, func(p string, d os.DirEntry, werr error) error {
				if werr != nil || d.IsDir() {
					return werr
				}
				rel, _ := filepath.Rel(dest, p)
				rel = filepath.ToSlash(rel)
				info, _ := d.Info()
				got[rel] = info.Mode().Perm()
				if b, _ := os.ReadFile(p); string(b) != "c:"+rel {
					t.Errorf("%s content = %q", rel, b)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("exported %v, want %v", got, tc.want)
			}
			for rel, mode := range tc.want {
				if got[rel] != mode {
					t.Errorf("%s mode = %v, want %v", rel, got[rel], mode)
				}
			}
			if st, err := os.Stat(dest); err != nil || st.Mode().Perm() != 0o755 {
				t.Errorf("dest stat = %v, %v", st, err)
			}
		})
	}
}

func TestExportPathsErrors(t *testing.T) {
	ctx := context.Background()
	g := New("")
	_, clone := gittest.NewRemote(t)
	sha := commitFiles(t, clone, map[string]fileSpec{".ard/config.yaml": {"x", 0o644}})

	t.Run("dest exists", func(t *testing.T) {
		dest := t.TempDir()
		if err := g.ExportPaths(ctx, clone, sha, dest, ".ard"); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("unknown sha", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "d")
		if err := g.ExportPaths(ctx, clone, "0123456789012345678901234567890123456789", dest, ".ard"); err == nil {
			t.Fatal("want error")
		}
		if _, err := os.Lstat(dest); !os.IsNotExist(err) {
			t.Errorf("dest left behind: %v", err)
		}
	})
	t.Run("gitattributes export-ignore does not drop files", func(t *testing.T) {
		_, c := gittest.NewRemote(t)
		s := commitFiles(t, c, map[string]fileSpec{
			".gitattributes":             {".ard/** export-ignore\n", 0o644},
			".ard/scanners/a/scan":       {"c:.ard/scanners/a/scan", 0o755},
			".ard/scanners/a/Dockerfile": {"c:.ard/scanners/a/Dockerfile", 0o644},
		})
		dest := filepath.Join(t.TempDir(), "d")
		if err := g.ExportPaths(ctx, c, s, dest, ".ard/scanners"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dest, ".ard/scanners/a/Dockerfile")); err != nil {
			t.Error(err)
		}
	})
}
