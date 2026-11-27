package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// countReader counts every GitHub read.
type countReader struct {
	Reader
	n int
}

func (r *countReader) ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error) {
	r.n++
	return r.Reader.ListIssues(ctx, o)
}

func (r *countReader) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	r.n++
	return r.Reader.GetIssue(ctx, n)
}

// jsonEscapeFirst writes s's first byte as a JSON \uXXXX escape, so the raw
// bytes no longer hold s but the decoded value does.
func jsonEscapeFirst(s string) string {
	return fmt.Sprintf("%cu%04X", '\\', s[0]) + s[1:]
}

// asLLM makes the harness role a prompt.txt scanner with its defaults.
func asLLM(r *config.Role) {
	r.ScanPath, r.PromptPath = "", ".ard/scanners/"+r.Name+"/prompt.txt"
	r.Autoclose, r.Route = false, config.RouteNone
}

func (h *harness) noRun() {
	h.t.Helper()
	if _, err := h.db.LastRun(h.ctx); !errors.Is(err, scannerdb.ErrNotFound) {
		h.t.Errorf("a run was recorded (err %v): the scanner must stay due", err)
	}
}

// An engine that cannot run the role skips before any store, GitHub call or
// slot, and records nothing.
func TestRunEngineNotReadySkips(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		agent        Engine
	}{
		{"no credential", "reason=no_model_credential", nil},
		{"k8s", "reason=executor_unsupported", Unavailable{Err: ErrExecutorUnsupported}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, asLLM)
			buf := capture(h)
			cr := &countReader{Reader: h.gh}
			h.p.Reader = cr
			opened := 0
			h.p.Stores = func(context.Context, string) (Store, error) { opened++; return h.store, nil }
			h.p.Engine = Engines{Exec: h.eng, Agent: tc.agent}
			res := h.mustRun()
			if res.Outcome != OutcomeSkipped || h.eng.calls != 0 || cr.n != 0 || opened != 0 || len(h.gh.Writes()) != 0 {
				t.Errorf("res = %+v, scans %d, reads %d, stores opened %d", res, h.eng.calls, cr.n, opened)
			}
			contains(t, buf.String(), tc.reason)
			h.noRun()
		})
	}
}

// Stops reported by Scan record no run, so the scanner stays due.
func TestRunScanStopsAreNotRecorded(t *testing.T) {
	for _, tc := range []struct {
		err     error
		outcome string
		log     string
	}{
		{fmt.Errorf("scanner arch: %w", ErrNoCredential), OutcomeSkipped, "reason=no_model_credential"},
		{ErrExecutorUnsupported, OutcomeSkipped, "reason=executor_unsupported"},
		{&limitStop{err: fmt.Errorf("%w: weekly", ErrUsageLimit)}, OutcomeLimited, "scanner.limited"},
	} {
		t.Run(tc.outcome+"/"+tc.log, func(t *testing.T) {
			h := newHarness(t, asLLM)
			buf := capture(h)
			h.eng.failNext(tc.err)
			res, err := h.run()
			if err != nil || res.Outcome != tc.outcome || len(h.gh.Writes()) != 0 {
				t.Errorf("res = %+v, err = %v", res, err)
			}
			contains(t, buf.String(), tc.log)
			h.noRun()
		})
	}
}

// Mentions are defused in an LLM scanner's titles and bodies, after the guard;
// a deterministic scanner's text is not touched by it.
func TestRunDefusesLLMMentions(t *testing.T) {
	for _, llm := range []bool{true, false} {
		t.Run(fmt.Sprint("llm=", llm), func(t *testing.T) {
			mod := func(*config.Role) {}
			if llm {
				mod = asLLM
			}
			h := newHarness(t, mod)
			f := fnd("a", SevHigh)
			f.Title, f.Body = "ping @octocat", "cc @octocat <!-- x -->"
			h.eng.set(f)
			h.mustRun()
			is := h.issue(1)
			body := is.Body
			zwsp := string(rune(0x200b))
			defused := strings.Contains(body, "@"+zwsp+"octocat") && strings.Contains(is.Title, zwsp)
			if defused != llm {
				t.Errorf("defused = %v, want %v; body %q, title %q", defused, llm, body, is.Title)
			}
		})
	}
}

// The guard sees the agent engine's decoded values: a JSON-escaped secret in
// findings.jsonl fails the run closed.
func TestRunAgentEngineEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		ok   bool
		base redact.Scanner // nil: the builtin pass
	}{
		{"files one issue", llmLine, true, nil},
		{"escaped secret", strings.Replace(llmLine, `"title":"t"`, `"title":"t `+jsonEscapeFirst(planted())+`"`, 1), false, nil},
		{"guard error", llmLine, false, &stubScanner{err: errors.New("scanner crashed")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok && strings.Contains(tc.line, planted()) {
				t.Fatal("the raw line must not hold the secret")
			}
			f := newAgentFixture(t)
			f.ex.write = writeFindings(tc.line)
			h := newHarness(t, func(r *config.Role) {
				*r = f.role
				r.MaxOpen, r.GroupSize, r.Route = 5, 10, config.RouteNone
			})
			h.cfg.Repo, h.cfg.SHA, h.cfg.MaxBudgetUSD, h.cfg.Sidecars = f.cfg.Repo, f.cfg.SHA, f.cfg.MaxBudgetUSD, f.cfg.Sidecars
			h.p.Engine = Engines{Exec: h.eng, Agent: f.eng}
			if tc.base != nil {
				h.p.GuardScanners = func(*config.Resolved) (redact.Scanner, redact.Scanner) { return tc.base, nil }
			}
			buf := capture(h)
			res := h.mustRun()
			if !tc.ok {
				if res.Outcome != OutcomeScanFailed || (tc.base == nil) != (res.GuardField == "title") || h.writes("CreateIssue") != 0 || len(h.com.paths()) != 0 {
					t.Errorf("res = %+v, creates %d", res, h.writes("CreateIssue"))
				}
				contains(t, buf.String(), "scanner.guard_")
				if strings.Contains(buf.String(), planted()) {
					t.Error("the secret reached the log")
				}
				return
			}
			if res.Outcome != OutcomeOK || res.Created != 1 {
				t.Fatalf("res = %+v", res)
			}
			if got := h.issue(1).Labels; len(got) != 1 || got[0] != labels.Scanner("arch") {
				t.Errorf("labels = %v, want only the scanner label (no ard:pending)", got)
			}
			if p := h.com.paths(); len(p) != 1 || p[0] != ".ard/1/issue.md" {
				t.Errorf("committed %v", p)
			}
			if r := h.lastRun(); r.Outcome != OutcomeOK || r.SHA != "abc" {
				t.Errorf("run = %+v", r)
			}
		})
	}
}
