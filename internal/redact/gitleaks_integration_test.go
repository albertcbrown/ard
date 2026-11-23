package redact

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// PinnedGitleaksVersion is the gitleaks release the integration test and
// production are expected to use.
const PinnedGitleaksVersion = "8.28.0"

// TestGitleaksReal runs the real pinned binary named by ARD_GITLEAKS_BIN.
func TestGitleaksReal(t *testing.T) {
	bin := os.Getenv("ARD_GITLEAKS_BIN")
	if bin == "" {
		t.Skip("ARD_GITLEAKS_BIN not set")
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimPrefix(strings.TrimSpace(string(out)), "v"); got != PinnedGitleaksVersion {
		t.Fatalf("gitleaks version %q, want pinned %q", got, PinnedGitleaksVersion)
	}
	s := Gitleaks(GitleaksOptions{Path: bin})
	planted := "ok\ntoken = " + plantedTokens()["aws-access-key"] + "\naws_secret_access_key = " + randStr(21, 40) + "\n"
	got, err := s.Scan(context.Background(), strings.NewReader(planted))
	if err != nil || len(got) == 0 {
		t.Errorf("planted: got %+v, %v; want a finding", got, err)
	}
	got, err = s.Scan(context.Background(), strings.NewReader("build ok\nall tests passed\n"))
	if err != nil || len(got) != 0 {
		t.Errorf("plain: got %+v, %v; want clean", got, err)
	}
}
