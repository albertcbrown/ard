package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

const streakMarker = "PLANTED-SECRET-MARKER-123"

// withStatus creates a status issue in the fake and wires the pipeline to it.
func withStatus(t *testing.T, h *harness) int {
	t.Helper()
	is, err := h.gh.CreateIssue(h.ctx, github.NewIssue{Title: "status"})
	if err != nil {
		t.Fatal(err)
	}
	h.p.StatusIssue = func() int { return is.Number }
	return is.Number
}

func comments(t *testing.T, h *harness, n int) []string {
	t.Helper()
	cs, err := h.gh.ListComments(h.ctx, n, h.clock.AddDate(-1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

type streakCase struct {
	name  string
	fail  func(h *harness)
	heal  func(h *harness)
	class string
}

func streakCases() []streakCase {
	return []streakCase{
		{"scan_failed", func(h *harness) { h.eng.failNext(&ScanError{SHA: "s", Exit: 1}) }, func(h *harness) {}, "scan_failed"},
		{"timeout", func(h *harness) { h.eng.failNext(&ScanError{SHA: "s", TimedOut: true}) }, func(h *harness) {}, "scan_failed"},
		{"max_open", func(h *harness) {
			h.role.MaxOpen = 1
			h.eng.set(fnd("a", SevHigh))
		}, func(h *harness) { h.role.MaxOpen = 5 }, "max_open"},
		{"guard", func(h *harness) { h.eng.set(plant(fnd("p", SevHigh), "title", planted())) },
			func(h *harness) { h.eng.set() }, ""},
	}
}

func TestStreakPostsOncePerStreak(t *testing.T) {
	for _, c := range streakCases() {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, nil)
			n := withStatus(t, h)
			if c.name == "max_open" {
				h.eng.set(fnd("a", SevHigh))
				h.mustRun() // files one issue
			}
			for i := 0; i < 3; i++ {
				c.fail(h)
				h.mustRun()
			}
			got := comments(t, h, n)
			if len(got) != 1 || !strings.Contains(got[0], "`cve`") || !strings.Contains(got[0], "failing") {
				t.Fatalf("after streak: %q", got)
			}
			if c.class != "" && !strings.Contains(got[0], "`"+c.class+"`") {
				t.Errorf("class missing: %q", got[0])
			}
			c.heal(h)
			if res := h.mustRun(); res.Outcome != OutcomeOK {
				t.Fatalf("heal run = %+v", res)
			}
			got = comments(t, h, n)
			if len(got) != 2 || !strings.Contains(got[1], "recovered") {
				t.Fatalf("after recovery: %q", got)
			}
			h.mustRun()
			if got = comments(t, h, n); len(got) != 2 {
				t.Errorf("repost after recovery: %q", got)
			}
		})
	}
}

func TestStreakSurvivesRestart(t *testing.T) {
	h := newHarness(t, nil)
	n := withStatus(t, h)
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	old := h.p
	np := *old // a restarted controller: new Pipeline, same on-disk store
	h.p = &np
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	if got := comments(t, h, n); len(got) != 1 {
		t.Fatalf("restart reposted: %q", got)
	}
	h.mustRun()
	if got := comments(t, h, n); len(got) != 2 || !strings.Contains(got[1], "recovered") {
		t.Fatalf("recovery: %q", got)
	}
}

func TestStreakNeutralOutcomes(t *testing.T) {
	h := newHarness(t, nil)
	n := withStatus(t, h)
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	h.cfg.GlobalMaxOpen = 0
	if res := h.mustRun(); res.Outcome != OutcomeGlobalCap {
		t.Fatalf("res = %+v", res)
	}
	h.cfg.GlobalMaxOpen = 30
	h.eng.failNext(errors.New("boom"))
	if res, _ := h.run(); res.Outcome != OutcomeError {
		t.Fatalf("res = %+v", res)
	}
	if got := comments(t, h, n); len(got) != 1 {
		t.Fatalf("neutral runs changed the streak: %q", got)
	}
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	if got := comments(t, h, n); len(got) != 1 {
		t.Fatalf("reposted: %q", got)
	}
}

func TestStreakNoFindingText(t *testing.T) {
	h := newHarness(t, nil)
	n := withStatus(t, h)
	f := fnd("p", SevHigh)
	f.Title += " " + streakMarker
	f.Body = streakMarker
	f.Fingerprint += "-" + streakMarker
	h.eng.set(plant(f, "title", planted()))
	h.mustRun() // guard hit
	h.eng.set()
	h.eng.failNext(&ScanError{Exit: 1, Err: errors.New(streakMarker)})
	h.mustRun()
	h.mustRun() // recovery
	got := comments(t, h, n)
	if len(got) != 2 {
		t.Fatalf("comments = %q", got)
	}
	for _, b := range got {
		if strings.Contains(b, streakMarker) || strings.Contains(b, planted()) || strings.Contains(b, "title p") {
			t.Errorf("finding text in post: %q", b)
		}
		if !strings.Contains(b, "`cve`") {
			t.Errorf("scanner name missing: %q", b)
		}
	}
}

func TestStreakDryRun(t *testing.T) {
	h := newHarness(t, nil)
	dry, _ := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	h.p.Effects = dry
	h.p.StatusIssue = func() int { return -1 }
	buf := capture(h)
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	if !strings.Contains(buf.String(), "scanner.streak_notice") {
		t.Errorf("notice not logged: %s", buf)
	}
	if v, err := h.db.Meta(h.ctx, streakKey); err == nil && v != "" {
		t.Errorf("state written in dry-run: %q", v)
	}
}

func TestStreakNoStatusIssue(t *testing.T) {
	h := newHarness(t, nil)
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun() // StatusIssue nil
	n := 0
	h.p.StatusIssue = func() int { return n }
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun() // still unknown
	if v, err := h.db.Meta(h.ctx, streakKey); err == nil && v != "" {
		t.Fatalf("state touched: %q", v)
	}
	n = withStatus(t, h)
	h.p.StatusIssue = func() int { return n }
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	if got := comments(t, h, n); len(got) != 1 {
		t.Fatalf("comments = %q", got)
	}
}

type failingComments struct {
	Effects
	fail bool
}

func (f *failingComments) CreateComment(ctx context.Context, n int, body string) (github.Comment, error) {
	if f.fail {
		return github.Comment{}, errors.New("github down")
	}
	return f.Effects.CreateComment(ctx, n, body)
}

func TestStreakPostFailure(t *testing.T) {
	h := newHarness(t, nil)
	n := withStatus(t, h)
	fc := &failingComments{Effects: h.fx, fail: true}
	h.p.Effects = fc
	h.eng.failNext(&ScanError{Exit: 1})
	res, err := h.run()
	if err != nil || res.Outcome != OutcomeScanFailed {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if v, err := h.db.Meta(h.ctx, streakKey); err == nil && v != "" {
		t.Fatalf("state set after failed post: %q", v)
	}
	fc.fail = false
	h.eng.failNext(&ScanError{Exit: 1})
	h.mustRun()
	if got := comments(t, h, n); len(got) != 1 {
		t.Fatalf("retry: %q", got)
	}
}

func TestStreakOldStateLoads(t *testing.T) {
	h := newHarness(t, nil)
	if _, err := h.db.Meta(h.ctx, streakKey); !errors.Is(err, scannerdb.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	n := withStatus(t, h)
	h.mustRun() // ok with no streak: nothing posted
	if got := comments(t, h, n); len(got) != 0 {
		t.Fatalf("comments = %q", got)
	}
}
