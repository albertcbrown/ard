package redact

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	DefaultMaxBytes  int64 = 1 << 20
	DefaultHeadBytes int64 = 32 << 10
	defaultFindings        = 100
)

// ErrBadOptions is returned by New for inconsistent cap options.
var ErrBadOptions = errors.New("redact: bad options")

// Options tune a Redactor. Zero values select the defaults.
type Options struct {
	MaxBytes    int64   // default DefaultMaxBytes (1 MiB)
	HeadBytes   int64   // default DefaultHeadBytes (32 KiB)
	Scanner     Scanner // default Builtin()
	MaxFindings int     // default 100; extra hits are counted, not stored
}

// Redactor is immutable after New and safe for concurrent use.
type Redactor struct {
	secrets *Secrets
	opts    Options
	chunk   int
	open    func(string) (io.ReadCloser, error) // test hook; default os.Open
}

// New returns a Redactor for the given known values.
func New(s *Secrets, o Options) (*Redactor, error) {
	if s == nil {
		s = NewSecrets()
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.HeadBytes == 0 {
		o.HeadBytes = DefaultHeadBytes
	}
	if o.Scanner == nil {
		o.Scanner = Builtin()
	}
	if o.MaxFindings == 0 {
		o.MaxFindings = defaultFindings
	}
	if o.MaxFindings < 0 {
		return nil, fmt.Errorf("max findings %d: %w", o.MaxFindings, ErrBadOptions)
	}
	if o.HeadBytes < 0 || o.MaxBytes < o.HeadBytes+markerReserve+1024 {
		return nil, fmt.Errorf("max %d, head %d: %w", o.MaxBytes, o.HeadBytes, ErrBadOptions)
	}
	return &Redactor{secrets: s, opts: o, chunk: defaultChunk, open: func(p string) (io.ReadCloser, error) { return os.Open(p) }}, nil
}

// LogResult is the verdict for one log.
type LogResult struct {
	Clean        []byte         // capped, redacted, scanned; nil unless the verdict is clean
	Flagged      bool           // a scan hit: commit nothing, add ard:needs-human
	Findings     []Finding      // from the full scan plus the re-scan, de-duplicated
	FindingCount int            // total, including findings beyond MaxFindings; saturates at the scanner cap (10000)
	Replaced     map[string]int // name -> count; for logging only
	Truncated    bool
	OrigBytes    int64  // raw size
	RedactedPath string // full redacted host-local log (dst)
}

// Log reads src (the raw combined log) and writes the full redacted log to dst
// atomically (temp file in dst's directory, mode 0600, fsync, rename). On a
// hit it returns Flagged with Clean == nil and a nil error. On any failure it
// returns an error with a zero result; the caller treats that like a hit and
// no dst or temp file is left behind.
func (r *Redactor) Log(ctx context.Context, src, dst string) (LogResult, error) {
	start := time.Now()
	in, err := r.open(src)
	if err != nil {
		return LogResult{}, fmt.Errorf("redact %s: %w", src, err)
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".redact-*.tmp")
	if err != nil {
		return LogResult{}, fmt.Errorf("redact %s: create temp: %w", src, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	bw := bufio.NewWriterSize(tmp, 64<<10)
	counts, orig, err := r.secrets.stream(ctx, in, bw, r.chunk)
	if err != nil {
		return LogResult{}, fmt.Errorf("redact %s: %w", src, err)
	}
	if err := bw.Flush(); err != nil {
		return LogResult{}, fmt.Errorf("redact %s: flush: %w", src, err)
	}
	st, err := tmp.Stat()
	if err != nil {
		return LogResult{}, fmt.Errorf("redact %s: stat: %w", src, err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return LogResult{}, fmt.Errorf("redact %s: seek: %w", src, err)
	}
	found, err := r.scan(ctx, tmp)
	if err != nil {
		return LogResult{}, fmt.Errorf("redact %s: scan: %w", src, err)
	}
	if err := tmp.Sync(); err != nil {
		return LogResult{}, fmt.Errorf("redact %s: sync: %w", src, err)
	}
	if err := tmp.Close(); err != nil {
		return LogResult{}, fmt.Errorf("redact %s: close: %w", src, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return LogResult{}, fmt.Errorf("redact %s: rename: %w", src, err)
	}
	renamed = true
	ok := false
	defer func() {
		if !ok {
			os.Remove(dst)
		}
	}()

	res := LogResult{Replaced: counts, OrigBytes: orig, RedactedPath: dst}
	if len(found) == 0 {
		f, err := os.Open(dst)
		if err != nil {
			return LogResult{}, fmt.Errorf("redact %s: open redacted: %w", src, err)
		}
		capped, trunc, err := capReader(f, st.Size(), r.opts.MaxBytes, r.opts.HeadBytes)
		f.Close()
		if err != nil {
			return LogResult{}, fmt.Errorf("redact %s: %w", src, err)
		}
		found, err = r.scan(ctx, bytes.NewReader(capped))
		if err != nil {
			return LogResult{}, fmt.Errorf("redact %s: rescan: %w", src, err)
		}
		res.Truncated = trunc
		if len(found) == 0 {
			res.Clean = capped
		}
	}
	ok = true
	res.Flagged = len(found) > 0
	res.FindingCount = len(found)
	res.Findings = limitFindings(found, r.opts.MaxFindings)

	rules := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		rules = append(rules, f.Rule)
	}
	sort.Strings(rules)
	slog.Info("redact.log", "src", src, "orig_bytes", res.OrigBytes, "out_bytes", len(res.Clean),
		"replaced", res.Replaced, "findings", res.FindingCount, "rules", rules,
		"truncated", res.Truncated, "flagged", res.Flagged, "dur", time.Since(start))
	return res, nil
}

// TextResult is the verdict for a small text.
type TextResult struct {
	Clean    []byte // nil when flagged
	Flagged  bool
	Findings []Finding
}

// Text redacts and scans b with no cap. Use it for role files, plan.md and
// summary comments.
func (r *Redactor) Text(ctx context.Context, b []byte) (TextResult, error) {
	out, _ := r.secrets.Bytes(b)
	found, err := r.scan(ctx, bytes.NewReader(out))
	if err != nil {
		return TextResult{}, fmt.Errorf("redact text: scan: %w", err)
	}
	if len(found) > 0 {
		return TextResult{Flagged: true, Findings: limitFindings(found, r.opts.MaxFindings)}, nil
	}
	return TextResult{Clean: out}, nil
}

func limitFindings(f []Finding, n int) []Finding {
	if len(f) > n {
		return f[:n]
	}
	return f
}

// scan runs the scanner so that a cancelled ctx fails closed even if the
// scanner ignores it.
func (r *Redactor) scan(ctx context.Context, rd io.Reader) ([]Finding, error) {
	type out struct {
		f   []Finding
		err error
	}
	ch := make(chan out, 1)
	go func() {
		f, err := r.opts.Scanner.Scan(ctx, rd)
		ch <- out{f, err}
	}()
	select {
	case o := <-ch:
		return o.f, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
