package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

// requiredReal are the captures that must be committed before this lands. The
// usage-limit capture is optional: it needs a live limit (testdata/CAPTURE.md).
var requiredReal = []string{"success", "error_max_turns", "budget_exceeded", "auth_bad_token"}

const pendingMarker = "testdata/REAL_FIXTURES_PENDING"

type realMeta struct {
	Case            string `json:"case"`
	ExitCode        int    `json:"exit_code"`
	Seconds         int    `json:"seconds"`
	CLIVersion      string `json:"cli_version"`
	APIRetries      int    `json:"api_retries"`
	RateLimitEvents int    `json:"rate_limit_events"`
	ResultSubtype   string `json:"result_subtype"`
}

func realFixtures(t *testing.T) []string {
	t.Helper()
	fs, err := filepath.Glob("testdata/real_*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(fs)
	return fs
}

func readMeta(t *testing.T, jsonl string) realMeta {
	t.Helper()
	b, err := os.ReadFile(strings.TrimSuffix(jsonl, ".jsonl") + ".meta.json")
	if err != nil {
		t.Fatalf("%s has no meta file: %v", jsonl, err)
	}
	var m realMeta
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("%s meta: %v", jsonl, err)
	}
	return m
}

// TestRealFixturesPresent is the draft gate. While testdata/REAL_FIXTURES_PENDING
// exists the required real captures may be missing; once they are committed the
// marker must be deleted, and without the marker a missing capture fails. The
// PR must not merge while the marker exists.
func TestRealFixturesPresent(t *testing.T) {
	var missing []string
	for _, c := range requiredReal {
		for _, ext := range []string{".jsonl", ".meta.json"} {
			if _, err := os.Stat("testdata/real_" + c + ext); err != nil {
				missing = append(missing, "real_"+c+ext)
			}
		}
	}
	_, markerErr := os.Stat(pendingMarker)
	pending := markerErr == nil
	switch {
	case pending && len(missing) == 0:
		t.Errorf("all required real fixtures are committed: delete %s", pendingMarker)
	case !pending && len(missing) > 0:
		t.Errorf("required real fixtures missing: %s (capture them: testdata/CAPTURE.md)", strings.Join(missing, ", "))
	case pending:
		t.Logf("DRAFT: real fixtures still to capture: %s", strings.Join(missing, ", "))
	}
}

// TestRealFixturesScrubbed applies the scrubber's checks to every committed
// real capture, so a hand edit or a stale scrub cannot slip a UUID, a home path
// or a credential in.
func TestRealFixturesScrubbed(t *testing.T) {
	for _, f := range realFixtures(t) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			again, err := scrubTranscript(b, scrubEnv{})
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(b) {
				t.Errorf("the file is not in scrubbed form (a placeholder or a path differs); re-run the scrub step")
			}
			readMeta(t, f)
		})
	}
}

func TestParseReal(t *testing.T) {
	for _, f := range realFixtures(t) {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "real_"), ".jsonl")
		t.Run(name, func(t *testing.T) {
			meta := readMeta(t, f)
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			r, perr := New(Options{}).Parse(strings.NewReader(string(in)), meta.ExitCode)
			if perr != nil && !strings.HasPrefix(name, "auth_") {
				t.Fatalf("Parse: %v", perr)
			}
			switch {
			case name == "success":
				if !r.OK || r.Subtype != "success" || r.CostUSD == nil || r.LimitHit != nil || r.CLIVersion != TestedVersion || r.SessionID != "session-1" {
					t.Errorf("result = %+v", r)
				}
				if r.Usage == nil || r.Usage.Input+r.Usage.Output == 0 || len(r.ModelUsage) == 0 {
					t.Errorf("usage = %+v / %+v: want tokens parsed from the real result", r.Usage, r.ModelUsage)
				}
			case name == "error_max_turns":
				if r.OK || r.Subtype != "error_max_turns" || r.LimitHit != nil {
					t.Errorf("result = %+v", r)
				}
			case name == "budget_exceeded":
				if r.OK || r.Subtype != "error_max_budget_usd" || r.LimitHit != nil {
					t.Errorf("result = %+v", r)
				}
			case strings.HasPrefix(name, "auth_"):
				if r.OK || r.Transient() || r.LimitHit != nil {
					t.Errorf("result = %+v", r)
				}
			case strings.HasPrefix(name, "usage_limit_"):
				scope := strings.TrimPrefix(name, "usage_limit_")
				if r.OK || r.LimitHit == nil {
					t.Fatalf("result = %+v", r)
				}
				got := string(r.LimitHit.Scope)
				switch r.LimitHit.Scope {
				case agent.ScopeModel:
					got = r.LimitHit.Family
				case agent.ScopeUnknown:
					got = "unknown" // named so by fixtureName in the capture harness
				}
				if got != scope {
					t.Errorf("scope = %q, want %q", got, scope)
				}
				if meta.RateLimitEvents > 0 && r.LimitHit.ResetsAt.IsZero() {
					t.Error("a rate_limit_event was captured but ResetsAt is zero")
				}
			default:
				t.Errorf("no expectations for real fixture %q: add them", name)
			}
		})
	}
}

// countEvents counts stream lines of a type (and subtype when non-empty).
func countEvents(raw []byte, typ, subtype string) int {
	n := 0
	for _, l := range bytes.Split(raw, []byte("\n")) {
		var h head
		if json.Unmarshal(l, &h) == nil && h.Type == typ && (subtype == "" || h.Subtype == subtype) {
			n++
		}
	}
	return n
}
