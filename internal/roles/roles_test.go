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
	"time"

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
	want := []string{"config.yaml", "reviewers/eng-review/prompt.txt", "reviewers/license-review/allowlist.txt", "reviewers/license-review/prompt.txt", "reviewers/product-manager-review/prompt.txt", "reviewers/security-review/prompt.txt", "scanners/cve/Dockerfile", "scanners/cve/merge.jq", "scanners/cve/scan", "scanners/docs/Dockerfile", "scanners/docs/docscan.go", "scanners/docs/scan", "scanners/error-codes/Dockerfile", "scanners/error-codes/errcodes.jq", "scanners/error-codes/scan", "scanners/trufflehog/Dockerfile", "scanners/trufflehog/exclude-paths", "scanners/trufflehog/findings.jq", "scanners/trufflehog/scan", "triage/architect/prompt.txt", "triage/designer/prompt.txt", "triage/documentation-planner/prompt.txt", "triage/engineer/prompt.txt", "triage/lead/prompt.txt", "triage/product-manager/prompt.txt", "workers/engineer/prompt.txt"}
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
	for _, p := range []string{"scanners/cve/scan", "scanners/docs/scan", "scanners/error-codes/scan", "scanners/trufflehog/scan"} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&0o111 == 0 {
			t.Fatalf("%s mode %v has no exec bit", p, fi.Mode())
		}
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
	// Without the chmod, config rejects the role: the exec bit really is needed.
	if _, err := config.LoadFS(starterFS(t, false), "/repo"); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("want an 'executable' error without chmod, got %v", err)
	}

	res, err := config.LoadFS(starterFS(t, true), "/repo")
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
	if found.Every != 6*time.Hour || found.MaxOpen != 10 || found.GroupSize != 1 {
		t.Errorf("cve overrides: every=%v max_open=%d group_size=%d, want 6h, 10, 1", found.Every, found.MaxOpen, found.GroupSize)
	}
	if found.Dockerfile != ".ard/scanners/cve/Dockerfile" {
		t.Errorf("Dockerfile = %q", found.Dockerfile)
	}
	if !slices.Equal(found.Extra, []string{"merge.jq"}) {
		t.Errorf("Extra = %v, want [merge.jq]", found.Extra)
	}
}

func TestEmbeddedErrorCodesRoleLoadsAsScanner(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	for _, r := range res.Roles {
		if r.Kind != config.KindScanner || r.Name != "error-codes" {
			continue
		}
		if r.ScanPath != ".ard/scanners/error-codes/scan" || !r.Autoclose || r.Every != 24*time.Hour || r.GroupSize != 20 || r.MaxOpen != 5 {
			t.Errorf("unexpected role: %+v", r)
		}
		if r.Dockerfile != ".ard/scanners/error-codes/Dockerfile" {
			t.Errorf("Dockerfile = %q", r.Dockerfile)
		}
		if !slices.Equal(r.Extra, []string{"errcodes.jq"}) {
			t.Errorf("Extra = %v, want [errcodes.jq]", r.Extra)
		}
		return
	}
	t.Fatal("no scanner role error-codes")
}

func TestEmbeddedDocsRoleLoadsAsScanner(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	r, ok := res.Role(config.KindScanner, "docs")
	if !ok {
		t.Fatalf("no scanner role docs in %+v", res.Roles)
	}
	if r.ScanPath != ".ard/scanners/docs/scan" || r.PromptPath != "" || !r.Autoclose {
		t.Errorf("unexpected role: %+v", r)
	}
	if r.Every != 72*time.Hour || r.MaxOpen != 3 || r.GroupSize != 10 {
		t.Errorf("docs overrides: every=%v max_open=%d group_size=%d, want 72h, 3, 10", r.Every, r.MaxOpen, r.GroupSize)
	}
	if r.Dockerfile != ".ard/scanners/docs/Dockerfile" {
		t.Errorf("Dockerfile = %q", r.Dockerfile)
	}
	if !slices.Equal(r.Extra, []string{"docscan.go"}) {
		t.Errorf("Extra = %v, want [docscan.go]", r.Extra)
	}
	if r.ReadsUntrusted() {
		t.Error("docs scanner must not read untrusted input")
	}
}

// starterFS lays the embedded starter out as a repo's .ard/ directory.
func starterFS(t *testing.T, chmod bool) fstest.MapFS {
	t.Helper()
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

func TestEmbeddedTrufflehogRoleLoadsAsScanner(t *testing.T) {
	res, err := config.LoadFS(starterFS(t, true), "/repo")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	for _, r := range res.Roles {
		if r.Kind != config.KindScanner || r.Name != "trufflehog" {
			continue
		}
		if r.ScanPath != ".ard/scanners/trufflehog/scan" || !r.Autoclose || r.Every != 24*time.Hour || r.GroupSize != 1 || r.MaxOpen != 5 || r.Timeout != 30*time.Minute {
			t.Errorf("unexpected role: %+v", r)
		}
		if r.Route != config.RouteNeedsHuman {
			t.Errorf("Route = %v, want needs-human", r.Route)
		}
		if r.Dockerfile != ".ard/scanners/trufflehog/Dockerfile" {
			t.Errorf("Dockerfile = %q", r.Dockerfile)
		}
		if !slices.Equal(r.Extra, []string{"exclude-paths", "findings.jq"}) {
			t.Errorf("Extra = %v, want [exclude-paths findings.jq]", r.Extra)
		}
		return
	}
	t.Fatal("no scanner role trufflehog")
}
