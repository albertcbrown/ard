package scanner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/redact"
)

// GuardHit is one guard hit. It never carries scanned text.
type GuardHit struct {
	Index int    // finding index in scan order
	Field string // title, fingerprint, group, files, body, or unknown
	Rule  string // builtin rule ID, "gitleaks:<id>" or "known-value:<ENV NAME>"
}

// GuardResult is the guard's verdict. It never carries scanned text.
type GuardResult struct {
	Hits []GuardHit // non-empty: the run changes nothing
	// OptionalErr is set when the optional pass (gitleaks) could not run while
	// ctx was live, and only when the base pass and known values were clean.
	OptionalErr error
}

type guardSpan struct {
	Index    int
	Field    string
	From, To int // 1-based lines, inclusive
}

// CheckFindings runs the pre-filing guard over every field of fs. The error
// is non-nil when the base pass failed or ctx ended; it never contains
// scanned text.
func CheckFindings(ctx context.Context, fs []Finding, base, optional redact.Scanner, sec *redact.Secrets) (GuardResult, error) {
	if base == nil {
		base = redact.Builtin()
	}
	var buf bytes.Buffer
	var spans []guardSpan
	var hits []GuardHit
	line := 1
	add := func(i int, field, value string) {
		to := line + strings.Count(value, "\n")
		spans = append(spans, guardSpan{Index: i, Field: field, From: line, To: to})
		for _, n := range sec.Names([]byte(value)) {
			hits = append(hits, GuardHit{Index: i, Field: field, Rule: "known-value:" + n})
		}
		buf.WriteString(value)
		buf.WriteByte('\n')
		line = to + 1
	}
	for i, f := range fs {
		add(i, "title", f.Title)
		add(i, "fingerprint", f.Fingerprint)
		if f.Group != "" {
			add(i, "group", f.Group)
		}
		for _, p := range f.Files {
			add(i, "files", p)
		}
		if f.Body != "" {
			add(i, "body", f.Body)
		}
	}
	data := buf.Bytes()

	found, err := guardScan(ctx, base, data)
	if err != nil {
		return GuardResult{}, fmt.Errorf("guard: builtin pass: %w", err)
	}
	hits = append(hits, mapHits(found, spans, "")...)
	if len(hits) > 0 {
		sortHits(hits)
		return GuardResult{Hits: hits}, nil
	}
	if optional == nil {
		return GuardResult{}, nil
	}
	found, err = guardScan(ctx, optional, data)
	if err != nil {
		if ctx.Err() != nil {
			return GuardResult{}, fmt.Errorf("guard: %w", ctx.Err())
		}
		return GuardResult{OptionalErr: fmt.Errorf("guard: gitleaks pass: %w", err)}, nil
	}
	hits = mapHits(found, spans, "gitleaks:")
	sortHits(hits)
	return GuardResult{Hits: hits}, nil
}

func mapHits(found []redact.Finding, spans []guardSpan, prefix string) []GuardHit {
	var out []GuardHit
	for _, f := range found {
		h := GuardHit{Index: -1, Field: "unknown", Rule: prefix + f.Rule}
		for _, s := range spans {
			if f.Line >= s.From && f.Line <= s.To {
				h.Index, h.Field = s.Index, s.Field
				break
			}
		}
		out = append(out, h)
	}
	return out
}

func sortHits(h []GuardHit) {
	sort.SliceStable(h, func(i, j int) bool {
		if h[i].Index != h[j].Index {
			return h[i].Index < h[j].Index
		}
		if h[i].Field != h[j].Field {
			return h[i].Field < h[j].Field
		}
		return h[i].Rule < h[j].Rule
	})
}

// guardScan runs s so that a cancelled ctx fails closed even if the scanner
// ignores it (a local copy of the redactor's pattern).
func guardScan(ctx context.Context, s redact.Scanner, data []byte) ([]redact.Finding, error) {
	type out struct {
		f   []redact.Finding
		err error
	}
	ch := make(chan out, 1)
	go func() {
		f, err := s.Scan(ctx, io.Reader(bytes.NewReader(data)))
		ch <- out{f, err}
	}()
	select {
	case o := <-ch:
		if o.err == nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return o.f, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
