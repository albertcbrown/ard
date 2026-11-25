package snapshot

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRingKeepsNewestInOrder(t *testing.T) {
	r := NewRing(3)
	if len(r.Events()) != 0 {
		t.Fatal("new ring not empty")
	}
	for i := 1; i <= 5; i++ {
		r.Append(Event{Msg: fmt.Sprint(i)})
		if i == 2 {
			if got := r.Events(); len(got) != 2 || got[0].Msg != "1" || got[1].Msg != "2" {
				t.Fatalf("partial = %+v", got)
			}
		}
	}
	got := r.Events()
	if len(got) != 3 || got[0].Msg != "3" || got[1].Msg != "4" || got[2].Msg != "5" {
		t.Fatalf("events = %+v", got)
	}
	got[0].Msg = "x" // a copy
	if r.Events()[0].Msg != "3" {
		t.Error("Events returned the internal buffer")
	}
}

func TestRingConcurrentAppend(t *testing.T) {
	r := NewRing(8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				r.Append(Event{Msg: "m"})
				_ = r.Events()
			}
		}()
	}
	wg.Wait()
	if n := len(r.Events()); n != 8 {
		t.Errorf("len = %d, want 8", n)
	}
}

func TestTeeForwardsByteIdentically(t *testing.T) {
	drive := func(h slog.Handler) {
		l := slog.New(h).With("loop", "worker").WithGroup("g").With("a", 1)
		l.Info("hello", "k", "v", slog.Group("inner", "x", 2))
		l.Warn("second", "err", fmt.Errorf("boom"))
	}
	var plain, teed bytes.Buffer
	opts := &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}}
	drive(slog.NewTextHandler(&plain, opts))
	ring := NewRing(10)
	drive(NewTee(slog.NewTextHandler(&teed, opts), ring, nil))
	if plain.String() != teed.String() {
		t.Fatalf("output differs:\n%s\n---\n%s", plain.String(), teed.String())
	}
	ev := ring.Events()
	if len(ev) != 2 || ev[0].Msg != "hello" || ev[0].Level != slog.LevelInfo {
		t.Fatalf("events = %+v", ev)
	}
	if want := "loop=worker g.a=1 g.k=v g.inner.x=2"; ev[0].Attrs != want {
		t.Errorf("attrs = %q, want %q", ev[0].Attrs, want)
	}
}

func TestTeeRedactsRecordedTextOnly(t *testing.T) {
	var out bytes.Buffer
	ring := NewRing(2)
	l := slog.New(NewTee(slog.NewTextHandler(&out, nil), ring, func(s string) string { return strings.ReplaceAll(s, "secret", "[redacted]") }))
	l.Info("secret", "k", "secret")
	if e := ring.Events()[0]; e.Msg != "[redacted]" || e.Attrs != "k=[redacted]" {
		t.Errorf("event = %+v", e)
	}
	if !bytes.Contains(out.Bytes(), []byte("msg=secret")) {
		t.Errorf("downstream output was altered: %s", out.String())
	}
}
