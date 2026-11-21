package config

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden(t *testing.T) {
	fsys := withConfig(t, `
agent: kimi
intervals: {scanners: 6h}
merge: {protected_paths: [infra/]}
sidecars:
  vpe-specs: {repo: acme/specs, ref: v1}
overrides:
  scanners/cve: {every: 12h, max_open: 10, group_size: 1}
  triage/lead: {sidecar: vpe-specs, after: [architect]}
  workers/engineer: {reviewers: [security-review]}
  reviewers/license-review: {when: {paths: [go.mod]}}
`)
	res := mustLoad(t, fsys)

	golden := filepath.Join("testdata", "resolved.golden")
	if *updateGolden {
		var buf bytes.Buffer
		if err := res.WriteYAML(&buf); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	var got bytes.Buffer
	if err := res.WriteYAML(&got); err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		t.Errorf("rendered config differs from golden %s:\n%s", golden, got.String())
	}
}
