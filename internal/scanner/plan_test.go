package scanner

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

const day = 24 * time.Hour

func row(fp string, issue int, st scannerdb.Status, sev Severity) Row {
	return Row{Fingerprint: fp, Issue: issue, Status: st, Severity: string(sev), Group: "g", FirstSeen: t0, LastSeen: t0, LastSHA: "old"}
}

func TestDiff(t *testing.T) {
	now := t0.Add(100 * day)
	snap := func(rows []Row, sups ...Suppression) Snapshot { return NewSnapshot(rows, sups) }
	f := func(fp string, sev Severity) Finding { return finding(fp, "t "+fp, sev, "g") }
	tests := []struct {
		name      string
		snap      Snapshot
		found     []Finding
		autoclose bool
		check     func(t *testing.T, p Plan)
	}{
		{"no row is new", snap(nil), []Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) { wantFPs(t, "New", p.New, "a") }},
		{"expired suppression is new", snap(nil, Suppression{Fingerprint: "a", Until: now.Add(-time.Hour), Severity: "low", Issue: 3}),
			[]Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) { wantFPs(t, "New", p.New, "a") }},
		{"active suppression same severity", snap(nil, Suppression{Fingerprint: "a", Until: now.Add(day), Severity: "high", Issue: 3}),
			[]Finding{f("a", SevHigh)}, true, func(t *testing.T, p Plan) {
				wantFPs(t, "Suppressed", p.Suppressed, "a")
				wantFPs(t, "New", p.New)
			}},
		{"active suppression lower severity", snap(nil, Suppression{Fingerprint: "a", Until: now.Add(day), Severity: "high", Issue: 3}),
			[]Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) { wantFPs(t, "Suppressed", p.Suppressed, "a") }},
		{"active suppression raised severity lifts it", snap(nil, Suppression{Fingerprint: "a", Until: now.Add(day), Severity: "medium", Issue: 3}),
			[]Finding{f("a", SevHigh)}, true, func(t *testing.T, p Plan) { wantFPs(t, "New", p.New, "a") }},
		{"suppression expiring exactly now is expired", snap(nil, Suppression{Fingerprint: "a", Until: now, Severity: "high"}),
			[]Finding{f("a", SevHigh)}, true, func(t *testing.T, p Plan) { wantFPs(t, "New", p.New, "a") }},
		{"fixed is a regression of the old issue", snap([]Row{row("a", 41, StatusFixed, SevLow)}),
			[]Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) {
				if len(p.Regress) != 1 || p.Regress[0].Of != 41 || p.Regress[0].Finding.Fingerprint != "a" {
					t.Errorf("Regress = %+v", p.Regress)
				}
			}},
		{"open and found is seen", snap([]Row{row("a", 5, StatusOpen, SevLow)}),
			[]Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) { wantFPs(t, "Seen", p.Seen, "a") }},
		{"open and absent is gone", snap([]Row{row("a", 5, StatusOpen, SevLow)}),
			nil, true, func(t *testing.T, p Plan) { wantStrs(t, "Gone", p.Gone, "a") }},
		{"gone and found resurfaces", snap([]Row{row("a", 5, StatusGone, SevLow)}),
			[]Finding{f("a", SevLow)}, true, func(t *testing.T, p Plan) { wantFPs(t, "Resurface", p.Resurface, "a") }},
		{"gone and absent is nothing", snap([]Row{row("a", 5, StatusGone, SevLow)}),
			nil, true, func(t *testing.T, p Plan) { wantStrs(t, "Gone", p.Gone) }},
		{"fixed and absent is nothing", snap([]Row{row("a", 5, StatusFixed, SevLow)}),
			nil, true, func(t *testing.T, p Plan) { wantStrs(t, "Gone", p.Gone) }},
		{"no autoclose: absence changes nothing", snap([]Row{row("a", 5, StatusOpen, SevLow)}),
			nil, false, func(t *testing.T, p Plan) { wantStrs(t, "Gone", p.Gone) }},
		{"no autoclose: a ticked line is just seen", snap([]Row{row("a", 5, StatusGone, SevLow)}),
			[]Finding{f("a", SevLow)}, false, func(t *testing.T, p Plan) {
				wantFPs(t, "Resurface", p.Resurface)
				wantFPs(t, "Seen", p.Seen, "a")
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, Diff(tt.snap, tt.found, tt.autoclose, now)) })
	}
}

func wantFPs(t *testing.T, what string, got []Finding, want ...string) {
	t.Helper()
	var fps []string
	for _, f := range got {
		fps = append(fps, f.Fingerprint)
	}
	wantStrs(t, what, fps, want...)
}

func wantStrs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(got)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func view(n int, group string, lines ...Line) IssueView {
	body := RenderBody("", Parsed{Scanner: "cve", Group: group, Lines: lines}, nil)
	return IssueView{Number: n, Labels: []string{"ard:scanner:cve", "ard:pending"}, CreatedAt: t0, Body: body, Parsed: ParseBody(body)}
}

func ln(fp string, checked bool, sev Severity) Line {
	return Line{Checked: checked, Title: "t " + fp, Fingerprint: fp, Severity: sev}
}

func closedAt(reason github.StateReason, at time.Time) Fate {
	return FateOf(github.Issue{State: github.StateClosed, StateReason: reason, ClosedAt: &at}, "")
}

func TestReconcile(t *testing.T) {
	sup := 90 * day
	closed := t0.Add(10 * day)
	tests := []struct {
		name  string
		snap  Snapshot
		open  []IssueView
		fates map[int]Fate
		check func(t *testing.T, b Batch)
	}{
		{"completed becomes a fixed tombstone on the same issue",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevHigh)}, nil), nil, map[int]Fate{5: closedAt(github.ReasonCompleted, closed)},
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 1 || b.PutFindings[0].Status != StatusFixed || b.PutFindings[0].Issue != 5 || len(b.DeleteFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"closed with no reason is completed",
			NewSnapshot([]Row{row("a", 5, StatusGone, SevHigh)}, nil), nil, map[int]Fate{5: closedAt("", closed)},
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 1 || b.PutFindings[0].Status != StatusFixed {
					t.Errorf("%+v", b)
				}
			}},
		{"not planned suppresses until closed_at plus suppress_for",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevHigh)}, nil), nil, map[int]Fate{5: closedAt(github.ReasonNotPlanned, closed)},
			func(t *testing.T, b Batch) {
				want := Suppression{Fingerprint: "a", Until: closed.Add(sup), Severity: "high", Issue: 5, Reason: "not_planned"}
				if !reflect.DeepEqual(b.PutSuppressions, []Suppression{want}) || !reflect.DeepEqual(b.DeleteFindings, []string{"a"}) || len(b.PutFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"duplicate suppresses like not planned",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevLow)}, nil), nil, map[int]Fate{5: closedAt(github.ReasonDuplicate, closed)},
			func(t *testing.T, b Batch) {
				if len(b.PutSuppressions) != 1 || b.PutSuppressions[0].Reason != "duplicate" || b.PutSuppressions[0].Until != closed.Add(sup) {
					t.Errorf("%+v", b)
				}
			}},
		{"a later issue's suppression is not overwritten by an older one",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevLow)}, []Suppression{{Fingerprint: "a", Until: closed, Severity: "low", Issue: 9}}),
			nil, map[int]Fate{5: closedAt(github.ReasonNotPlanned, closed)},
			func(t *testing.T, b Batch) {
				if len(b.PutSuppressions) != 0 || len(b.DeleteFindings) != 1 {
					t.Errorf("%+v", b)
				}
			}},
		{"deleted or transferred drops the row",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevLow)}, nil), nil, map[int]Fate{5: {Kind: FateGone}},
			func(t *testing.T, b Batch) {
				if !reflect.DeepEqual(b.DeleteFindings, []string{"a"}) || len(b.PutSuppressions) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"still open but unlabelled keeps tracking",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevLow)}, nil), nil, map[int]Fate{5: {Kind: FateOpen}},
			func(t *testing.T, b Batch) {
				if len(b.PutFindings)+len(b.DeleteFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"a failed lookup leaves the row alone",
			NewSnapshot([]Row{row("a", 5, StatusOpen, SevLow)}, nil), nil, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings)+len(b.DeleteFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"reopened lifts the suppression and re-tracks the row",
			NewSnapshot(nil, []Suppression{{Fingerprint: "a", Until: closed.Add(sup), Severity: "low", Issue: 5}}),
			[]IssueView{view(5, "g", ln("a", false, SevLow))}, nil,
			func(t *testing.T, b Batch) {
				if !reflect.DeepEqual(b.DeleteSuppressions, []string{"a"}) || len(b.PutFindings) != 1 || b.PutFindings[0].Status != StatusOpen || b.PutFindings[0].Issue != 5 {
					t.Errorf("%+v", b)
				}
			}},
		{"a marker on an open issue missing from the db is adopted",
			NewSnapshot(nil, nil), []IssueView{view(7, "g", ln("a", false, SevHigh), ln("b", true, SevLow))}, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 2 || b.PutFindings[0].Fingerprint != "a" || b.PutFindings[0].Status != StatusOpen ||
					b.PutFindings[1].Status != StatusGone || b.PutFindings[0].Group != "g" || b.PutFindings[0].Severity != "high" || !b.PutFindings[0].FirstSeen.Equal(t0) {
					t.Errorf("%+v", b.PutFindings)
				}
			}},
		{"an up-to-date row is not rewritten",
			NewSnapshot([]Row{row("a", 7, StatusOpen, SevHigh)}, nil), []IssueView{view(7, "g", ln("a", false, SevHigh))}, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"a human ticking a box makes the row gone",
			NewSnapshot([]Row{row("a", 7, StatusOpen, SevHigh)}, nil), []IssueView{view(7, "g", ln("a", true, SevHigh))}, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 1 || b.PutFindings[0].Status != StatusGone || b.PutFindings[0].LastSHA != "old" {
					t.Errorf("%+v", b)
				}
			}},
		{"a fingerprint on two open issues goes to the lowest number",
			NewSnapshot(nil, nil), []IssueView{view(9, "g", ln("a", false, SevLow)), view(4, "g", ln("a", false, SevLow))}, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings) != 1 || b.PutFindings[0].Issue != 4 {
					t.Errorf("%+v", b.PutFindings)
				}
			}},
		{"a line deleted from an open issue drops its row",
			NewSnapshot([]Row{row("a", 7, StatusOpen, SevHigh), row("b", 7, StatusOpen, SevHigh)}, nil),
			[]IssueView{view(7, "g", ln("a", false, SevHigh))}, nil,
			func(t *testing.T, b Batch) {
				if !reflect.DeepEqual(b.DeleteFindings, []string{"b"}) {
					t.Errorf("%+v", b)
				}
			}},
		{"synthetic issues are never touched",
			NewSnapshot([]Row{row("a", -3, StatusOpen, SevLow)}, nil), nil, map[int]Fate{-3: {Kind: FateGone}},
			func(t *testing.T, b Batch) {
				if len(b.PutFindings)+len(b.DeleteFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
		{"fixed rows are left alone",
			NewSnapshot([]Row{row("a", 3, StatusFixed, SevLow)}, nil), nil, nil,
			func(t *testing.T, b Batch) {
				if len(b.PutFindings)+len(b.DeleteFindings) != 0 {
					t.Errorf("%+v", b)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Reconcile(tt.snap, tt.open, tt.fates, sup, t0.Add(50*day))
			tt.check(t, res.Batch)
		})
	}
}

func TestReconcileReportsDuplicates(t *testing.T) {
	res := Reconcile(NewSnapshot(nil, nil), []IssueView{view(1, "g", ln("a", false, SevLow)), view(2, "g", ln("a", false, SevLow))}, nil, day, t0)
	if !reflect.DeepEqual(res.Duplicates, []string{"a"}) {
		t.Errorf("Duplicates = %v", res.Duplicates)
	}
}

func TestReconcileIsIdempotent(t *testing.T) {
	snap := NewSnapshot([]Row{row("a", 5, StatusOpen, SevHigh), row("b", 6, StatusOpen, SevLow)}, nil)
	fates := map[int]Fate{5: closedAt(github.ReasonNotPlanned, t0.Add(day)), 6: closedAt(github.ReasonCompleted, t0)}
	first := Reconcile(snap, nil, fates, 90*day, t0.Add(30*day))
	snap.Apply(first.Batch)
	second := Reconcile(snap, nil, fates, 90*day, t0.Add(60*day)) // later now: must not move the suppression
	if len(second.Batch.PutFindings)+len(second.Batch.DeleteFindings)+len(second.Batch.PutSuppressions)+len(second.Batch.DeleteSuppressions) != 0 {
		t.Errorf("second pass changed things: %+v", second.Batch)
	}
}

func TestFateOf(t *testing.T) {
	at := t0
	tests := []struct {
		name string
		is   github.Issue
		repo string
		want FateKind
	}{
		{"open", github.Issue{State: github.StateOpen}, "", FateOpen},
		{"completed", github.Issue{State: github.StateClosed, StateReason: github.ReasonCompleted, ClosedAt: &at}, "", FateCompleted},
		{"reopened then closed without a reason", github.Issue{State: github.StateClosed, StateReason: github.ReasonReopened}, "", FateCompleted},
		{"not planned", github.Issue{State: github.StateClosed, StateReason: github.ReasonNotPlanned}, "", FateSuppressed},
		{"duplicate", github.Issue{State: github.StateClosed, StateReason: github.ReasonDuplicate}, "", FateSuppressed},
		{"transferred", github.Issue{State: github.StateOpen, URL: "https://github.com/other/repo/issues/5"}, "o/r", FateGone},
		{"same repo, other case", github.Issue{State: github.StateOpen, URL: "https://github.com/O/R/issues/5"}, "o/r", FateOpen},
	}
	for _, tt := range tests {
		if got := FateOf(tt.is, tt.repo).Kind; got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}

func items(spec ...string) []Item { // "fp:sev:group" or "fp:sev:group:reg"
	var out []Item
	for _, s := range spec {
		var fp, sev, g string
		var reg int
		if n, _ := fmt.Sscanf(s, "%s %s %s %d", &fp, &sev, &g, &reg); n < 3 {
			panic(s)
		}
		if g == "-" {
			g = ""
		}
		out = append(out, Item{F: finding(fp, "t "+fp, Severity(sev), g), RegressionOf: reg})
	}
	return out
}

func opSummary(ops []GroupOp) []string {
	var out []string
	for _, op := range ops {
		var fps []string
		for _, it := range op.Items {
			fps = append(fps, it.F.Fingerprint)
		}
		kind := "new"
		if op.Join != 0 {
			kind = fmt.Sprintf("join#%d", op.Join)
		}
		out = append(out, fmt.Sprintf("%s[%s]%v", kind, op.Group, fps))
	}
	return out
}

func TestPlanGroups(t *testing.T) {
	tests := []struct {
		name     string
		items    []Item
		joinable []Joinable
		size     int
		budget   int
		want     []string
		over     int
	}{
		{"join with room", items("a low g", "b low g"), []Joinable{{Number: 5, Group: "g", Lines: 8}}, 10, 5,
			[]string{"join#5[g][a b]"}, 0},
		{"overflow starts a new issue", items("a low g", "b low g", "c low g"), []Joinable{{Number: 5, Group: "g", Lines: 9}}, 10, 5,
			[]string{"join#5[g][a]", "new[g][b c]"}, 0},
		{"checked lines count against the size", items("a low g"), []Joinable{{Number: 5, Group: "g", Lines: 10}}, 10, 5,
			[]string{"new[g][a]"}, 0},
		{"another group's issue is not joined", items("a low g"), []Joinable{{Number: 5, Group: "h", Lines: 1}}, 10, 5,
			[]string{"new[g][a]"}, 0},
		{"lowest joinable number first", items("a low g", "b low g"), []Joinable{{Number: 9, Group: "g", Lines: 9}, {Number: 3, Group: "g", Lines: 9}}, 10, 5,
			[]string{"join#3[g][a]", "join#9[g][b]"}, 0},
		{"empty group: one issue per finding", items("a low -", "b low -", "c high -"), nil, 10, 5,
			[]string{"new[][c]", "new[][a]", "new[][b]"}, 0},
		{"regressions never join", items("a low g 41", "b low g"), []Joinable{{Number: 5, Group: "g", Lines: 1}}, 10, 5,
			[]string{"join#5[g][b]", "new[g][a]"}, 0},
		{"a regression's new issue may also hold new findings", items("a low g 41", "b low g", "c low g"), nil, 10, 5,
			[]string{"new[g][a b c]"}, 0},
		{"chunks of group_size", items("a low g", "b low g", "c low g", "d low g", "e low g"), nil, 2, 5,
			[]string{"new[g][a b]", "new[g][c d]", "new[g][e]"}, 0},
		{"budget 0 files nothing", items("a low g", "b high -"), nil, 10, 0, nil, 2},
		{"budget cut-off keeps severity order", items("a low g", "b critical -", "c high -", "d medium h"), nil, 10, 2,
			[]string{"new[][b]", "new[][c]"}, 2},
		{"joins do not consume budget", items("a low g", "b low h"), []Joinable{{Number: 5, Group: "g", Lines: 1}}, 10, 1,
			[]string{"join#5[g][a]", "new[h][b]"}, 0},
		{"groups ordered by best severity then key", items("a low g", "b high h", "c high f"), nil, 10, 5,
			[]string{"new[f][c]", "new[h][b]", "new[g][a]"}, 0},
		{"a zero group size is treated as one", items("a low g", "b low g"), nil, 0, 5,
			[]string{"new[g][a]", "new[g][b]"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops, over := PlanGroups(tt.items, tt.joinable, tt.size, tt.budget)
			got := opSummary(ops)
			if !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Errorf("ops = %v, want %v", got, tt.want)
			}
			if over != tt.over {
				t.Errorf("over budget = %d, want %d", over, tt.over)
			}
		})
	}
}

func TestPlanGroupsIsDeterministic(t *testing.T) {
	its := items("e low g", "d high g", "c low h", "b high h", "a low -")
	first, _ := PlanGroups(its, nil, 2, 9)
	for i := 0; i < 20; i++ {
		shuffled := append([]Item(nil), its...)
		sort.Slice(shuffled, func(a, b int) bool { return (a*7+i)%5 < (b*3+i)%5 })
		got, _ := PlanGroups(shuffled, nil, 2, 9)
		if !reflect.DeepEqual(opSummary(got), opSummary(first)) {
			t.Fatalf("order depends on input order: %v vs %v", opSummary(got), opSummary(first))
		}
	}
}
