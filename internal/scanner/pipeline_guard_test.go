package scanner

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/redact"
)

// capture swaps the harness logger for one that records everything.
func capture(h *harness) *bytes.Buffer {
	var buf bytes.Buffer
	h.p.Log = slog.New(slog.NewTextHandler(&buf, nil))
	return &buf
}

func plant(f Finding, field, v string) Finding {
	switch field {
	case "title":
		f.Title += " " + v
	case "body":
		f.Body = "line one\n" + v
	case "files":
		f.Files = []string{"ok.go", "dir/" + v}
	case "fingerprint":
		f.Fingerprint += "-" + v
	case "group":
		f.Group = "grp-" + v
	}
	return f
}

func TestGuardBlocks(t *testing.T) {
	v := planted()
	for _, field := range []string{"title", "body", "files", "fingerprint", "group"} {
		for _, sc := range []string{"create", "join", "autoclose", "rerate"} {
			t.Run(field+"/"+sc, func(t *testing.T) {
				h := newHarness(t, nil)
				x := Finding{Fingerprint: "x", Title: "title x", Severity: SevHigh}
				var second []Finding
				switch sc {
				case "create":
					second = []Finding{plant(fnd("p", SevHigh), field, v)}
				case "join":
					x.Group = "G"
					h.eng.set(x)
					h.mustRun()
					p := fnd("p", SevHigh)
					p.Group = "G"
					second = []Finding{x, plant(p, field, v)}
				case "autoclose":
					y := fnd("y", SevHigh)
					h.eng.set(x, y)
					h.mustRun()
					second = []Finding{plant(x, field, v)}
				case "rerate":
					h.eng.set(x)
					h.mustRun()
					r := x
					r.Severity = SevLow
					second = []Finding{plant(r, field, v)}
				}
				if sc == "create" {
					h.eng.set(fnd("seed", SevHigh))
					h.mustRun()
				}
				rowsBefore := map[string]Row{}
				for _, fp := range []string{"x", "y", "seed"} {
					if r, ok := h.row(fp); ok {
						rowsBefore[fp] = r
					}
				}
				writes, commits := len(h.gh.Writes()), len(h.com.reqs)
				buf := capture(h)
				h.eng.set(second...)
				res, err := h.run()
				if err != nil {
					t.Fatal(err)
				}
				if res.Outcome != OutcomeScanFailed || res.GuardField != field {
					t.Fatalf("res = %+v", res)
				}
				if got := len(h.gh.Writes()); got != writes {
					t.Errorf("%d new writes", got-writes)
				}
				if len(h.com.reqs) != commits {
					t.Error("committer called")
				}
				for fp, want := range rowsBefore {
					if got, _ := h.row(fp); got != want {
						t.Errorf("row %s changed: %+v -> %+v", fp, want, got)
					}
				}
				if r := h.lastRun(); r.Outcome != OutcomeScanFailed {
					t.Errorf("run row = %+v", r)
				}
				for _, bad := range []string{v, "title p", "title x " + v} {
					if strings.Contains(buf.String(), bad) {
						t.Errorf("log contains %q", bad)
					}
				}
			})
		}
	}
}

func TestGuardDryRun(t *testing.T) {
	h := newHarness(t, nil)
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.eng.set(plant(fnd("p", SevHigh), "title", planted()))
	res := h.mustRun()
	if res.Outcome != OutcomeScanFailed {
		t.Fatalf("res = %+v", res)
	}
	if _, ok := h.row("p"); ok {
		t.Error("row written")
	}
	if is, _ := dry.Issues(); len(is) != 0 {
		t.Errorf("ledger has %d issues", len(is))
	}
}

func TestGuardBaseError(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	writes := len(h.gh.Writes())
	buf := capture(h)
	h.p.GuardScanners = func(*config.Resolved) (redact.Scanner, redact.Scanner) {
		return &stubScanner{err: errors.New("boom")}, nil
	}
	h.eng.set() // a is gone: autoclose must stay blocked
	res := h.mustRun()
	if res.Outcome != OutcomeScanFailed || res.GuardRule != "scan-error" || len(h.gh.Writes()) != writes {
		t.Fatalf("res = %+v, %d writes", res, len(h.gh.Writes())-writes)
	}
	contains(t, buf.String(), "scanner.guard_failed")
	contains(t, buf.String(), "pass=builtin")
	contains(t, buf.String(), "reason=error")
}

func TestGuardGitleaksMissing(t *testing.T) {
	for _, block := range []bool{false, true} {
		for _, real := range []bool{false, true} {
			t.Run(fmt.Sprintf("block=%v/real=%v", block, real), func(t *testing.T) {
				h := newHarness(t, nil)
				h.eng.set(fnd("g1", SevHigh), gfnd("p1", SevHigh, "G"), gfnd("p2", SevHigh, "G"), fnd("r", SevHigh))
				h.mustRun()
				issueOf := func(fp string) int { r, _ := h.row(fp); return r.Issue }
				whole, part := issueOf("g1"), issueOf("p1")
				writes := len(h.gh.Writes())
				h.cfg.Redact.Gitleaks = config.Gitleaks{Enabled: true, Path: "/nonexistent/gitleaks", BlockAutocloseWhenMissing: block}
				if !real {
					h.p.GuardScanners = func(*config.Resolved) (redact.Scanner, redact.Scanner) {
						return nil, &stubScanner{err: errors.New("no binary")}
					}
				}
				buf := capture(h)
				low := fnd("r", SevLow)
				h.eng.set(gfnd("p2", SevHigh, "G"), low, fnd("n", SevHigh))
				res := h.mustRun()
				if res.Outcome != OutcomeScanFailed || res.GuardRule != "scan-error" {
					t.Fatalf("res = %+v", res)
				}
				if _, ok := h.row("n"); ok {
					t.Error("new finding recorded")
				}
				if r, _ := h.row("r"); r.Severity != string(SevHigh) {
					t.Errorf("re-rated: %+v", r)
				}
				if block {
					contains(t, buf.String(), "autoclose=blocked")
					if len(h.gh.Writes()) != writes || res.Closed != 0 {
						t.Errorf("wrote %d, closed %d", len(h.gh.Writes())-writes, res.Closed)
					}
					return
				}
				contains(t, buf.String(), "autoclose=allowed")
				if res.Closed != 1 || h.issue(whole).State != github.StateClosed || h.issue(part).State != github.StateOpen {
					t.Errorf("res = %+v", res)
				}
				if h.writes("CreateIssue") != 3 || h.writes("UpdateIssueBody") != 0 {
					t.Errorf("creates %d, body updates %d", h.writes("CreateIssue"), h.writes("UpdateIssueBody"))
				}
				if got := h.issue(part).Body; strings.Contains(got, "[x]") {
					t.Errorf("partly gone issue edited:\n%s", got)
				}
				if r, _ := h.row("p1"); r.Status != StatusOpen {
					t.Errorf("p1 = %+v", r)
				}
			})
		}
	}
}

func TestGuardCancelled(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.set(fnd("a", SevHigh))
	ctx, cancel := context.WithCancel(h.ctx)
	h.p.GuardScanners = func(*config.Resolved) (redact.Scanner, redact.Scanner) {
		return &stubScanner{run: cancel}, nil
	}
	_, err := h.p.Run(ctx, h.cfg, h.role, h.clock)
	if err == nil {
		t.Fatal("want an error")
	}
	if len(h.gh.Writes()) != 0 {
		t.Error("wrote")
	}
	if _, rerr := h.db.LastRun(h.ctx); rerr == nil {
		t.Error("a run was recorded")
	}
}

func TestGuardKnownValue(t *testing.T) {
	val := "zq" + "Wx7Lm2" + "Pv9Rt4Yk"
	h := newHarness(t, nil)
	sec := redact.NewSecrets()
	if err := sec.Add("ARD_TEST_TOKEN", val); err != nil {
		t.Fatal(err)
	}
	h.p.Secrets = func() *redact.Secrets { return sec }
	buf := capture(h)
	f := fnd("a", SevHigh)
	f.Body = "enc " + base64.StdEncoding.EncodeToString([]byte(val))
	h.eng.set(f)
	res := h.mustRun()
	if res.Outcome != OutcomeScanFailed || res.GuardRule != "known-value:ARD_TEST_TOKEN" || res.GuardField != "body" {
		t.Fatalf("res = %+v", res)
	}
	for _, bad := range []string{val, base64.StdEncoding.EncodeToString([]byte(val))} {
		if strings.Contains(buf.String(), bad) {
			t.Error("log carries the value")
		}
	}
}

func TestGuardProductionDefault(t *testing.T) {
	h := newHarness(t, nil)
	h.p.Secrets = nil
	h.eng.set(plant(fnd("a", SevHigh), "title", planted()))
	if res := h.mustRun(); res.Outcome != OutcomeScanFailed || h.writes("CreateIssue") != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestRouteCreate(t *testing.T) {
	for route, want := range map[config.Route][]string{
		config.RoutePending:    {labels.Scanner("cve"), labels.Pending},
		config.RouteNone:       {labels.Scanner("cve")},
		config.RouteNeedsHuman: {labels.Scanner("cve"), labels.NeedsHuman},
	} {
		t.Run(string(route), func(t *testing.T) {
			h := newHarness(t, func(r *config.Role) { r.Route = route })
			h.eng.set(fnd("a", SevHigh))
			h.mustRun()
			if got := h.issue(1).Labels; strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("labels = %v, want %v", got, want)
			}
		})
	}
}

func TestRouteBad(t *testing.T) {
	for _, route := range []config.Route{"", "bogus"} {
		h := newHarness(t, func(r *config.Role) { r.Route = route })
		buf := capture(h)
		h.eng.set(fnd("a", SevHigh))
		res, err := h.run()
		if err != nil || res.Outcome != OutcomeError || h.eng.calls != 0 || len(h.gh.Writes()) != 0 {
			t.Errorf("route %q: res = %+v, err = %v", route, res, err)
		}
		contains(t, buf.String(), "scanner.bad_route")
	}
}

func TestDryRunSyntheticJoinNeedsHuman(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.Route = config.RouteNeedsHuman })
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.eng.set(gfnd("a", SevHigh, "g"))
	h.mustRun()
	h.eng.set(gfnd("a", SevHigh, "g"), gfnd("b", SevLow, "g"))
	if res := h.mustRun(); res.Joined != 1 || res.Created != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestAutocloseNeedsHuman(t *testing.T) {
	h := newHarness(t, func(r *config.Role) { r.Route = config.RouteNeedsHuman })
	h.eng.set(fnd("a", SevHigh))
	h.mustRun()
	h.eng.set()
	if res := h.mustRun(); res.Closed != 1 || h.issue(1).State != github.StateClosed {
		t.Fatalf("res = %+v", res)
	}
}

func TestJoinableIssue(t *testing.T) {
	sc := labels.Scanner("cve")
	p := Parsed{HasSpan: true, Group: "g"}
	cases := []struct {
		name  string
		route string
		lbls  []string
		group string
		want  bool
	}{
		{"pending exact", labels.Pending, []string{sc, labels.Pending}, "g", true},
		{"none exact", "", []string{sc}, "g", true},
		{"needs-human exact", labels.NeedsHuman, []string{sc, labels.NeedsHuman}, "g", true},
		{"mixed case", labels.Pending, []string{"ARD:Scanner:cve", "ard:Pending"}, "g", true},
		{"unrelated label", "", []string{sc, "bug"}, "g", true},
		{"priority label", "", []string{sc, labels.P0}, "g", true},
		{"priority label with route", labels.Pending, []string{sc, labels.Pending, "ARD:P3"}, "g", true},
		{"none plus pending", "", []string{sc, labels.Pending}, "g", false},
		{"needs-human plus pending", labels.NeedsHuman, []string{sc, labels.NeedsHuman, labels.Pending}, "g", false},
		{"pending plus needs-human", labels.Pending, []string{sc, labels.Pending, labels.NeedsHuman}, "g", false},
		{"hold", "", []string{sc, labels.Hold}, "g", false},
		{"running", labels.Pending, []string{sc, labels.Pending, "ard:triage:lead:running"}, "g", false},
		{"missing route label", labels.Pending, []string{sc}, "g", false},
		{"wrong group", "", []string{sc}, "h", false},
		{"no scanner label", "", []string{}, "g", false},
	}
	for _, c := range cases {
		if got := joinableIssue(c.lbls, "cve", c.route, p, c.group); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
