package roles

import (
	"io/fs"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/config"
)

func TestFSContents(t *testing.T) {
	var got []string
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got = append(got, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"scanners/cve/Dockerfile", "scanners/cve/merge.jq", "scanners/cve/scan"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("embedded files = %v, want %v", got, want)
	}
	for _, p := range got {
		if strings.HasSuffix(p, "_test.go") || strings.Contains(p, "testdata") {
			t.Errorf("%s must not be embedded: ard init would copy it into target repos", p)
		}
	}
}

// embed.FS reports 0444 for everything, so the exec bit must be checked on disk
// to catch a mode lost in git.
func TestScanIsExecutableOnDisk(t *testing.T) {
	fi, err := os.Stat("scanners/cve/scan")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Fatalf("scanners/cve/scan mode %v has no exec bit", fi.Mode())
	}
}

func TestExecutableNames(t *testing.T) {
	if len(ExecutableNames) != 1 || ExecutableNames[0] != "scan" {
		t.Fatalf("ExecutableNames = %v", ExecutableNames)
	}
}

// This is what #27's writer must do: embed.FS drops modes (everything is 0444),
// so files named in ExecutableNames are written 0755.
func TestEmbeddedCveRoleLoadsAsScanner(t *testing.T) {
	asArdDir := func(chmod bool) fstest.MapFS {
		m := fstest.MapFS{}
		err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := FS.ReadFile(p)
			if err != nil {
				return err
			}
			mode := fs.FileMode(0o644)
			if chmod && slices.Contains(ExecutableNames, path.Base(p)) {
				mode = 0o755
			}
			m[".ard/"+p] = &fstest.MapFile{Data: b, Mode: mode}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// Without the chmod, config rejects the role: the exec bit really is needed.
	if _, err := config.LoadFS(asArdDir(false), "/repo"); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("want an 'executable' error without chmod, got %v", err)
	}

	res, err := config.LoadFS(asArdDir(true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	var found *config.Role
	for i := range res.Roles {
		if res.Roles[i].Kind == config.KindScanner && res.Roles[i].Name == "cve" {
			found = &res.Roles[i]
		}
	}
	if found == nil {
		t.Fatalf("no scanner role cve in %+v", res.Roles)
	}
	if found.ScanPath != ".ard/scanners/cve/scan" || found.PromptPath != "" || !found.Autoclose {
		t.Errorf("unexpected role: %+v", found)
	}
	if found.Dockerfile != ".ard/scanners/cve/Dockerfile" {
		t.Errorf("Dockerfile = %q", found.Dockerfile)
	}
	if !slices.Equal(found.Extra, []string{"merge.jq"}) {
		t.Errorf("Extra = %v, want [merge.jq]", found.Extra)
	}
}
