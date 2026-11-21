package roles

import (
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
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

// TODO(#27): once internal/config (#11) lands, copy FS into an fstest.MapFS
// under .ard/ with scan at 0o755 and check config.LoadFS yields a scanner role
// "cve" with ScanPath set.
