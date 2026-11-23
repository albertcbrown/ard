package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/redact"
)

// planted builds a credential-shaped value at run time.
func planted() string { return "AK" + "IA" + "QXZ7K3M2P9R4T6V8" }

type stubScanner struct {
	calls int
	f     []redact.Finding
	err   error
	run   func()
}

func (s *stubScanner) Scan(context.Context, io.Reader) ([]redact.Finding, error) {
	s.calls++
	if s.run != nil {
		s.run()
	}
	return s.f, s.err
}

func TestCheckFindingsSpanMapping(t *testing.T) {
	v := planted()
	clean := Finding{Fingerprint: "fp", Title: "t"}
	cases := []struct {
		name  string
		f     Finding
		field string
	}{
		{"title", Finding{Fingerprint: "fp", Title: v}, "title"},
		{"fingerprint", Finding{Fingerprint: v, Title: "t"}, "fingerprint"},
		{"group", Finding{Fingerprint: "fp", Title: "t", Group: v}, "group"},
		{"files", Finding{Fingerprint: "fp", Title: "t", Files: []string{"a", v}}, "files"},
		{"body line 3", Finding{Fingerprint: "fp", Title: "t", Body: "x\ny\n" + v}, "body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gr, err := CheckFindings(context.Background(), []Finding{clean, c.f}, nil, nil, nil)
			if err != nil || len(gr.Hits) != 1 {
				t.Fatalf("gr = %+v, err = %v", gr, err)
			}
			if h := gr.Hits[0]; h.Index != 1 || h.Field != c.field {
				t.Errorf("hit = %+v", h)
			}
			if strings.Contains(fmt.Sprintf("%+v", gr), v) {
				t.Error("result carries the planted value")
			}
		})
	}
	gr, err := CheckFindings(context.Background(), []Finding{clean}, nil, nil, redact.NewSecrets())
	if err != nil || len(gr.Hits) != 0 || gr.OptionalErr != nil {
		t.Errorf("clean: %+v %v", gr, err)
	}
}

func TestCheckFindingsStubs(t *testing.T) {
	ctx := context.Background()
	fs := []Finding{{Fingerprint: "fp", Title: "t"}}

	t.Run("out of range line is unknown and counts", func(t *testing.T) {
		base := &stubScanner{f: []redact.Finding{{Rule: "r", Line: 99}}}
		gr, err := CheckFindings(ctx, fs, base, nil, nil)
		if err != nil || len(gr.Hits) != 1 || gr.Hits[0].Field != "unknown" {
			t.Errorf("gr = %+v, err = %v", gr, err)
		}
	})
	t.Run("base hits skip the optional pass", func(t *testing.T) {
		base := &stubScanner{f: []redact.Finding{{Rule: "r", Line: 1}}}
		opt := &stubScanner{}
		gr, _ := CheckFindings(ctx, fs, base, opt, nil)
		if len(gr.Hits) != 1 || opt.calls != 0 {
			t.Errorf("hits %d, optional calls %d", len(gr.Hits), opt.calls)
		}
	})
	t.Run("optional hit is prefixed", func(t *testing.T) {
		opt := &stubScanner{f: []redact.Finding{{Rule: "r", Line: 2}}}
		gr, _ := CheckFindings(ctx, fs, &stubScanner{}, opt, nil)
		if len(gr.Hits) != 1 || gr.Hits[0].Rule != "gitleaks:r" || gr.Hits[0].Field != "fingerprint" {
			t.Errorf("gr = %+v", gr)
		}
	})
	t.Run("optional error with live ctx", func(t *testing.T) {
		gr, err := CheckFindings(ctx, fs, &stubScanner{}, &stubScanner{err: errors.New("boom")}, nil)
		if err != nil || gr.OptionalErr == nil || len(gr.Hits) != 0 {
			t.Errorf("gr = %+v, err = %v", gr, err)
		}
	})
	t.Run("optional error with cancelled ctx", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		opt := &stubScanner{err: errors.New("boom"), run: cancel}
		if _, err := CheckFindings(cctx, fs, &stubScanner{}, opt, nil); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("base error", func(t *testing.T) {
		if _, err := CheckFindings(ctx, fs, &stubScanner{err: errors.New("boom")}, nil, nil); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("known value", func(t *testing.T) {
		sec := redact.NewSecrets()
		val := "zq" + "Wx7Lm2" + "Pv9Rt4Yk"
		_ = sec.Add("ARD_TEST_TOKEN", val)
		gr, err := CheckFindings(ctx, []Finding{{Fingerprint: "fp", Title: "t", Body: "see " + val}}, nil, nil, sec)
		if err != nil || len(gr.Hits) != 1 || gr.Hits[0].Rule != "known-value:ARD_TEST_TOKEN" || gr.Hits[0].Field != "body" {
			t.Errorf("gr = %+v, err = %v", gr, err)
		}
	})
}
