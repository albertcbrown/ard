package pool

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func cand(c Class, role string, n int, age int) Candidate {
	return Candidate{Class: c, Role: role, Number: n, Since: t0.Add(time.Duration(age) * time.Minute)}
}

func newPool(max int, held ...Held) (*Pool, *clock) {
	ck := &clock{t: t0.Add(1000 * time.Hour)}
	return New(Options{Max: max, Now: ck.now, Held: held}), ck
}

func verdicts(ds []Decision) []Verdict {
	out := make([]Verdict, len(ds))
	for i, d := range ds {
		out[i] = d.Verdict
	}
	return out
}

func eq(a, b []Verdict) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClass(t *testing.T) {
	for i, c := range []Class{ClassReview, ClassWorker, ClassTriage, ClassScanner} {
		if int(c) != i {
			t.Errorf("%v = %d, want %d (persisted value)", c, int(c), i)
		}
		got, err := ParseClass(c.String())
		if err != nil || got != c {
			t.Errorf("ParseClass(%q) = %v, %v", c.String(), got, err)
		}
	}
	for _, s := range []string{"merger", "", "Review", "reaper"} {
		if _, err := ParseClass(s); err == nil {
			t.Errorf("ParseClass(%q) succeeded", s)
		}
	}
	if Class(9).String() != "class(9)" {
		t.Error("unknown class string")
	}
	if KeyFor(ClassTriage, "lead", 42) != "triage/lead/42" {
		t.Error("KeyFor")
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		c    Candidate
	}{
		{"bad class", Candidate{Class: 9, Role: "r", Number: 1, Since: t0}},
		{"empty role", Candidate{Class: ClassWorker, Number: 1, Since: t0}},
		{"zero since", Candidate{Class: ClassWorker, Role: "r", Number: 1}},
		{"scanner number", Candidate{Class: ClassScanner, Role: "r", Number: 1, Since: t0}},
		{"worker no number", Candidate{Class: ClassWorker, Role: "r", Since: t0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newPool(1)
			ds := p.Offer("l", time.Hour, []Candidate{tt.c})
			if ds[0].Verdict != DeniedInvalid || ds[0].Slot != nil || ds[0].Reason == "" {
				t.Fatalf("got %+v", ds[0])
			}
			if st := p.Stats(); st.Held != 0 || len(st.WaitingBy) != 0 {
				t.Fatalf("invalid candidate consumed state: %+v", st)
			}
		})
	}
	// Negative numbers are valid.
	p, _ := newPool(1)
	if ds := p.Offer("l", 0, []Candidate{cand(ClassWorker, "r", -3, 0)}); ds[0].Verdict != Granted {
		t.Fatalf("negative number: %+v", ds[0])
	}
}

func permutations(cs []Candidate) [][]Candidate {
	if len(cs) <= 1 {
		return [][]Candidate{append([]Candidate(nil), cs...)}
	}
	var out [][]Candidate
	for i := range cs {
		rest := append(append([]Candidate(nil), cs[:i]...), cs[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]Candidate{cs[i]}, p...))
		}
	}
	return out
}

func TestOrdering(t *testing.T) {
	tests := []struct {
		name string
		a, b Candidate // a must outrank b
	}{
		{"review beats older worker", cand(ClassReview, "r", 1, 50), cand(ClassWorker, "w", 2, 0)},
		{"worker beats triage", cand(ClassWorker, "w", 9, 50), cand(ClassTriage, "t", 1, 0)},
		{"triage beats older scanner", cand(ClassTriage, "t", 9, 50), cand(ClassScanner, "s", 0, 0)},
		{"older wins in class", cand(ClassWorker, "w", 9, 1), cand(ClassWorker, "w", 1, 2)},
		{"lower number on tie", cand(ClassWorker, "w", 1, 1), cand(ClassWorker, "w", 2, 1)},
		{"role on tie", cand(ClassTriage, "a", 1, 1), cand(ClassTriage, "b", 1, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !less(tt.a, tt.b) || less(tt.b, tt.a) {
				t.Fatal("wrong order")
			}
		})
	}
	// Max=1: the winner is the same for every permutation of the batch.
	batch := []Candidate{
		cand(ClassWorker, "w", 3, 0), cand(ClassReview, "r", 4, 9),
		cand(ClassReview, "r", 2, 9), cand(ClassScanner, "s", 0, 0),
	}
	for _, perm := range permutations(batch) {
		p, _ := newPool(1)
		for _, d := range p.Offer("l", 0, perm) {
			if (d.Verdict == Granted) != (d.Candidate.Key() == "review/r/2") {
				t.Fatalf("perm %v: unexpected %v for %s", perm, d.Verdict, d.Candidate.Key())
			}
		}
	}
}

func TestCapsAndRelease(t *testing.T) {
	p, _ := newPool(3)
	var cs []Candidate
	for i := 1; i <= 5; i++ {
		cs = append(cs, cand(ClassWorker, "w", i, 10-i)) // 5 is oldest
	}
	ds := p.Offer("l", 0, cs)
	want := []Verdict{DeniedFull, DeniedFull, Granted, Granted, Granted}
	if !eq(verdicts(ds), want) {
		t.Fatalf("got %v want %v", verdicts(ds), want)
	}
	for i, d := range ds {
		if (d.Slot != nil) != (d.Verdict == Granted) || d.Candidate != cs[i] {
			t.Fatalf("decision %d: %+v", i, d)
		}
	}
	if got := p.Offer("l", 0, cs[:2]); got[0].Verdict != DeniedFull || got[1].Verdict != DeniedFull {
		t.Fatalf("second offer: %v", verdicts(got))
	}
	if !ds[2].Slot.Release() {
		t.Fatal("first release false")
	}
	got := p.Offer("l", 0, cs[:2]) // 2 is older than 1
	if !eq(verdicts(got), []Verdict{DeniedFull, Granted}) {
		t.Fatalf("after release: %v", verdicts(got))
	}
}

func TestDuplicate(t *testing.T) {
	p, _ := newPool(3)
	c := cand(ClassWorker, "w", 1, 0)
	ds := p.Offer("l", 0, []Candidate{c, c})
	if !eq(verdicts(ds), []Verdict{Granted, DeniedDuplicate}) {
		t.Fatalf("batch: %v", verdicts(ds))
	}
	if ds := p.Offer("l", 0, []Candidate{c}); ds[0].Verdict != DeniedDuplicate {
		t.Fatalf("held: %v", ds[0].Verdict)
	}
}

func TestTriagePerIssue(t *testing.T) {
	a, b := cand(ClassTriage, "a", 7, 0), cand(ClassTriage, "b", 7, 1)
	t.Run("batch", func(t *testing.T) {
		p, _ := newPool(3)
		ds := p.Offer("l", 0, []Candidate{b, a})
		if !eq(verdicts(ds), []Verdict{DeniedIssueBusy, Granted}) {
			t.Fatalf("%v", verdicts(ds))
		}
	})
	t.Run("held", func(t *testing.T) {
		p, _ := newPool(3, Held{Candidate: a, JobID: "j"})
		if ds := p.Offer("l", 0, []Candidate{b}); ds[0].Verdict != DeniedIssueBusy {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("other issue and worker unaffected", func(t *testing.T) {
		p, _ := newPool(5, Held{Candidate: a})
		ds := p.Offer("l", 0, []Candidate{cand(ClassTriage, "a", 8, 0), cand(ClassWorker, "w", 7, 0)})
		if !eq(verdicts(ds), []Verdict{Granted, Granted}) {
			t.Fatalf("%v", verdicts(ds))
		}
	})
	t.Run("busy consumes no slot", func(t *testing.T) {
		p, _ := newPool(2)
		ds := p.Offer("l", 0, []Candidate{a, b, cand(ClassTriage, "a", 8, 2)})
		if !eq(verdicts(ds), []Verdict{Granted, DeniedIssueBusy, Granted}) {
			t.Fatalf("%v", verdicts(ds))
		}
	})
}

func TestReleaseSemantics(t *testing.T) {
	p, _ := newPool(1)
	c := cand(ClassWorker, "w", 1, 0)
	s1 := p.Offer("l", 0, []Candidate{c})[0].Slot
	if !s1.Release() || s1.Release() {
		t.Fatal("release should be true then false")
	}
	s2 := p.Offer("l", 0, []Candidate{c})[0].Slot
	if s2 == nil {
		t.Fatal("regrant failed")
	}
	if s1.Release() || p.Stats().Held != 1 {
		t.Fatal("stale handle freed a re-granted slot")
	}
	s2.SetJobID("job1")
	if s2.JobID() != "job1" || s2.Key() != c.Key() || s2.Candidate() != c {
		t.Fatal("slot accessors")
	}
	if got, ok := p.Lookup(c.Key()); !ok || got != s2 {
		t.Fatal("lookup")
	}
	s2.Release()
	if _, ok := p.Lookup(c.Key()); ok {
		t.Fatal("lookup after release")
	}

	p, _ = newPool(2)
	s := p.Offer("l", 0, []Candidate{c})[0].Slot
	var wg sync.WaitGroup
	var mu sync.Mutex
	trues := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Release() {
				mu.Lock()
				trues++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if trues != 1 || p.Stats().Held != 0 {
		t.Fatalf("trues=%d held=%d", trues, p.Stats().Held)
	}
}

func TestHeldAndSetMax(t *testing.T) {
	h := func(n int) Held { return Held{Candidate: cand(ClassWorker, "w", n, 0), JobID: fmt.Sprint("j", n)} }
	p, _ := newPool(2, h(1), h(2), h(3), h(1))
	if st := p.Stats(); st.Held != 3 || st.Free != 0 {
		t.Fatalf("%+v", st)
	}
	if ds := p.Offer("l", 0, []Candidate{cand(ClassReview, "r", 1, 0)}); ds[0].Verdict != DeniedFull {
		t.Fatalf("%v", ds[0].Verdict)
	}
	s, ok := p.Lookup(h(2).Candidate.Key())
	if !ok || s.JobID() != "j2" {
		t.Fatal("adopted lookup")
	}
	s.Release()
	if ds := p.Offer("l", 0, []Candidate{cand(ClassReview, "r", 1, 0)}); ds[0].Verdict != DeniedFull {
		t.Fatalf("still over max: %v", ds[0].Verdict)
	}

	p, _ = newPool(3, h(1), h(2), h(3))
	p.SetMax(1)
	c := cand(ClassReview, "r", 1, 0)
	if p.Stats().Held != 3 || p.Offer("l", 0, []Candidate{c})[0].Verdict != DeniedFull {
		t.Fatal("SetMax revoked or granted")
	}
	p.Lookup(h(1).Candidate.Key())
	for _, n := range []int{1, 2} {
		s, _ := p.Lookup(h(n).Candidate.Key())
		s.Release()
	}
	if p.Offer("l", 0, []Candidate{c})[0].Verdict != DeniedFull {
		t.Fatal("1 held at max 1 should deny")
	}
	s, _ = p.Lookup(h(3).Candidate.Key())
	s.Release()
	if p.Offer("l", 0, []Candidate{c})[0].Verdict != Granted {
		t.Fatal("should grant after releases")
	}
	p.SetMax(0)
	if p.Stats().Max != 1 {
		t.Fatal("SetMax(0) not clamped")
	}
}

func TestCrossLoop(t *testing.T) {
	rev := cand(ClassReview, "r", 1, 0)
	scan := cand(ClassScanner, "cve", 0, 0)
	setup := func() (*Pool, *clock, []*Slot) {
		p, ck := newPool(3)
		var cs []Candidate
		for i := 1; i <= 4; i++ {
			cs = append(cs, cand(ClassReview, "r", 10+i, i))
		}
		ds := p.Offer("review", 10*time.Minute, cs)
		var held []*Slot
		for _, d := range ds {
			if d.Slot != nil {
				held = append(held, d.Slot)
			}
		}
		if len(held) != 3 || ds[3].Verdict != DeniedFull {
			t.Fatalf("setup: %v", verdicts(ds))
		}
		held[0].Release()
		return p, ck, held
	}

	t.Run("outranked then reviewer granted", func(t *testing.T) {
		p, _, _ := setup()
		ds := p.Offer("scan", time.Hour, []Candidate{scan})
		if ds[0].Verdict != DeniedOutranked {
			t.Fatalf("%v", ds[0].Verdict)
		}
		ds = p.Offer("review", 10*time.Minute, []Candidate{cand(ClassReview, "r", 14, 4)})
		if ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("ttl expiry", func(t *testing.T) {
		p, ck, _ := setup()
		ck.t = ck.t.Add(11 * time.Minute)
		if ds := p.Offer("scan", time.Hour, []Candidate{scan}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("empty re-offer clears", func(t *testing.T) {
		p, _, _ := setup()
		p.Offer("review", 10*time.Minute, nil)
		if ds := p.Offer("scan", time.Hour, []Candidate{scan}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("held key does not reserve", func(t *testing.T) {
		p, _, _ := setup()
		// Another loop gets the waiting reviewer granted directly.
		p.Offer("other", 0, []Candidate{cand(ClassReview, "r", 14, 4)})
		if got := p.Stats().Held; got != 3 {
			t.Fatalf("held %d", got)
		}
		s, _ := p.Lookup("review/r/12")
		s.Release()
		if ds := p.Offer("scan", time.Hour, []Candidate{scan}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("lower class does not block higher", func(t *testing.T) {
		p, _ := newPool(1)
		held := p.Offer("a", 0, []Candidate{cand(ClassWorker, "w", 1, 0)})[0].Slot
		p.Offer("scan", time.Hour, []Candidate{scan})
		held.Release()
		if ds := p.Offer("work", time.Hour, []Candidate{cand(ClassWorker, "w", 2, 0)}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("reserves only up to free", func(t *testing.T) {
		p, _ := newPool(2)
		a := p.Offer("a", 0, []Candidate{cand(ClassWorker, "w", 1, 0), cand(ClassWorker, "w", 2, 0)})
		p.Offer("review", time.Hour, []Candidate{rev})
		a[0].Slot.Release()
		a[1].Slot.Release()
		ds := p.Offer("work", time.Hour, []Candidate{cand(ClassWorker, "w", 3, 0)})
		if ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("reserved triage issue already held", func(t *testing.T) {
		p, _ := newPool(2)
		ws := p.Offer("w", 0, []Candidate{cand(ClassWorker, "w", 1, 0), cand(ClassWorker, "w", 2, 0)})
		if ds := p.Offer("triage", time.Hour, []Candidate{cand(ClassTriage, "a", 7, 0)}); ds[0].Verdict != DeniedFull {
			t.Fatalf("%v", ds[0].Verdict)
		}
		ws[0].Slot.Release()
		if ds := p.Offer("other", 0, []Candidate{cand(ClassTriage, "b", 7, -1)}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
		ws[1].Slot.Release()
		if ds := p.Offer("scan", time.Hour, []Candidate{scan}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("one reservation per triage issue", func(t *testing.T) {
		p, _ := newPool(2)
		ws := p.Offer("w", 0, []Candidate{cand(ClassWorker, "w", 1, 0), cand(ClassWorker, "w", 2, 0)})
		p.Offer("triage", time.Hour, []Candidate{cand(ClassTriage, "a", 7, 0), cand(ClassTriage, "b", 7, 1)})
		ws[0].Slot.Release()
		ws[1].Slot.Release()
		if ds := p.Offer("scan", time.Hour, []Candidate{scan}); ds[0].Verdict != Granted {
			t.Fatalf("%v", ds[0].Verdict)
		}
	})
	t.Run("full vs outranked split", func(t *testing.T) {
		p, _ := newPool(2)
		ws := p.Offer("w", 0, []Candidate{cand(ClassWorker, "w", 1, 0), cand(ClassWorker, "w", 2, 0)})
		p.Offer("review", time.Hour, []Candidate{rev})
		ws[0].Slot.Release()
		ws[1].Slot.Release()
		ds := p.Offer("work", time.Hour, []Candidate{cand(ClassWorker, "w", 3, 0), cand(ClassWorker, "w", 4, 1), cand(ClassWorker, "w", 5, 2)})
		if !eq(verdicts(ds), []Verdict{Granted, DeniedOutranked, DeniedFull}) {
			t.Fatalf("%v", verdicts(ds))
		}
	})
}

func TestStarvation(t *testing.T) {
	var buf bytes.Buffer
	ck := &clock{t: t0.Add(1000 * time.Hour)}
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := New(Options{Max: 1, Now: ck.now, Log: log})
	w := p.Offer("w", 0, []Candidate{cand(ClassWorker, "w", 1, 0)})[0].Slot
	scan := cand(ClassScanner, "cve", 0, 0)
	n := func() int { return strings.Count(buf.String(), "pool.starved") }

	p.Offer("scan", 72*time.Hour, []Candidate{scan})
	ck.t = ck.t.Add(25 * time.Hour)
	p.Offer("scan", 72*time.Hour, []Candidate{scan})
	if n() != 1 {
		t.Fatalf("want 1 warning, got %d", n())
	}
	if w := p.Stats().OldestWait[ClassScanner]; w != 25*time.Hour {
		t.Fatalf("oldest wait %v", w)
	}
	ck.t = ck.t.Add(time.Hour)
	p.Offer("scan", 72*time.Hour, []Candidate{scan})
	if n() != 1 {
		t.Fatalf("repeat logged: %d", n())
	}
	w.Release()
	if p.Offer("scan", 72*time.Hour, []Candidate{scan})[0].Slot == nil {
		t.Fatal("scan not granted")
	}
	p.Lookup(scan.Key())
	s, _ := p.Lookup(scan.Key())
	s.Release()
	w = p.Offer("w", 0, []Candidate{cand(ClassWorker, "w", 1, 0)})[0].Slot
	p.Offer("scan", 72*time.Hour, []Candidate{scan})
	ck.t = ck.t.Add(25 * time.Hour)
	p.Offer("scan", 72*time.Hour, []Candidate{scan})
	if n() != 2 {
		t.Fatalf("want re-armed warning, got %d", n())
	}
	_ = w
}

func TestStats(t *testing.T) {
	p, _ := newPool(2)
	ds := p.Offer("a", time.Hour, []Candidate{
		cand(ClassReview, "r", 1, 0), cand(ClassWorker, "w", 1, 0), cand(ClassTriage, "t", 1, 0), cand(ClassScanner, "s", 0, 0),
	})
	_ = ds
	st := p.Stats()
	if st.Max != 2 || st.Held != 2 || st.Free != 0 || st.HeldBy[ClassReview] != 1 || st.HeldBy[ClassWorker] != 1 ||
		st.WaitingBy[ClassTriage] != 1 || st.WaitingBy[ClassScanner] != 1 {
		t.Fatalf("%+v", st)
	}
	st.HeldBy[ClassReview] = 99
	st.WaitingBy[ClassTriage] = 99
	if st2 := p.Stats(); st2.HeldBy[ClassReview] != 1 || st2.WaitingBy[ClassTriage] != 1 {
		t.Fatal("stats alias pool state")
	}
}

func TestConcurrency(t *testing.T) {
	const max = 3
	p, _ := newPool(max)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 200; i++ {
				c := cand(ClassWorker, fmt.Sprint("w", g), i, i)
				for _, d := range p.Offer(fmt.Sprint("loop", g), time.Minute, []Candidate{c}) {
					if got := p.Stats().Held; got > max {
						t.Errorf("held %d > %d", got, max)
					}
					if d.Slot != nil {
						d.Slot.Release()
					}
				}
			}
		}()
	}
	wg.Wait()
	if p.Stats().Held != 0 {
		t.Fatalf("held %d at end", p.Stats().Held)
	}
}
