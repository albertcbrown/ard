package roles

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/scanner"
)

// TestFixturesPassTheGuard runs the pre-filing guard over every golden scanner
// output: a real scanner's findings must not trip it. With ARD_GITLEAKS_BIN
// set, the gitleaks pass runs too.
func TestFixturesPassTheGuard(t *testing.T) {
	var optional redact.Scanner
	if bin := os.Getenv("ARD_GITLEAKS_BIN"); bin != "" {
		optional = redact.Gitleaks(redact.GitleaksOptions{Path: bin})
	}
	var files []string
	for _, kind := range []string{"cve", "docs"} {
		m, err := filepath.Glob(filepath.Join("testdata", kind, "*", "want.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			fs, err := scanner.DecodeFindings(bytes.NewReader(readFixture(t, f, "/R")))
			if err != nil {
				t.Fatal(err)
			}
			gr, err := scanner.CheckFindings(context.Background(), fs, redact.Builtin(), optional, redact.NewSecrets())
			if err != nil || len(gr.Hits) != 0 || gr.OptionalErr != nil {
				t.Errorf("guard: hits %+v, optional %v, err %v", gr.Hits, gr.OptionalErr, err)
			}
		})
	}
}
