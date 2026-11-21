package redact

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

const defaultChunk = 64 << 10

// stream copies r to w with every known value replaced. Memory is
// O(chunk + longest variant); there is no line limit. It returns the
// replacement count per name and the number of bytes read.
func (s *Secrets) stream(ctx context.Context, r io.Reader, w io.Writer, chunk int) (map[string]int, int64, error) {
	if chunk < 1 {
		chunk = defaultChunk
	}
	counts := map[string]int{}
	var total int64
	buf := make([]byte, s.maxLen+chunk)
	have := 0 // carried bytes at the start of buf
	eof := false
	for !eof {
		if err := ctx.Err(); err != nil {
			return nil, total, fmt.Errorf("redact stream: %w", err)
		}
		if have+chunk > len(buf) {
			buf = append(buf, make([]byte, have+chunk-len(buf))...)
		}
		nr, rerr := r.Read(buf[have : have+chunk])
		total += int64(nr)
		if rerr == io.EOF {
			eof = true
		} else if rerr != nil {
			return nil, total, fmt.Errorf("redact stream: read: %w", rerr)
		}
		b := buf[:have+nr]
		cut := len(b)
		if !eof && s.maxLen > 0 {
			cut = len(b) - (s.maxLen - 1)
		}
		if cut < 0 {
			cut = 0
		}
		last := 0
		stop := -1 // match start that must wait for more data
		for i := 0; i < cut && s.maxLen > 0; {
			if len(s.byFirst[b[i]]) > 0 {
				if v := s.matchAt(b, i); v != nil {
					end, ok := s.spanEnd(b, i, v, eof)
					if !ok {
						stop = i
						break
					}
					if _, err := w.Write(b[last:i]); err != nil {
						return nil, total, fmt.Errorf("redact stream: write: %w", err)
					}
					if _, err := io.WriteString(w, marker(v.name)); err != nil {
						return nil, total, fmt.Errorf("redact stream: write: %w", err)
					}
					counts[v.name]++
					i = end
					last = i
					continue
				}
			}
			i++
		}
		flush := last
		if stop >= 0 {
			if _, err := w.Write(b[last:stop]); err != nil {
				return nil, total, fmt.Errorf("redact stream: write: %w", err)
			}
			flush = stop
		} else if last < cut {
			if _, err := w.Write(b[last:cut]); err != nil {
				return nil, total, fmt.Errorf("redact stream: write: %w", err)
			}
			flush = cut
		}
		have = copy(buf, b[flush:])
	}
	return counts, total, nil
}

// Bytes redacts b in memory and returns the result with replacement counts
// per name.
func (s *Secrets) Bytes(b []byte) ([]byte, map[string]int) {
	var out bytes.Buffer
	counts, _, err := s.stream(context.Background(), bytes.NewReader(b), &out, defaultChunk)
	if err != nil { // cannot happen: in-memory reader and writer
		return nil, nil
	}
	return out.Bytes(), counts
}

// String redacts v. Its signature matches the func(string) string hook the
// effect layer (#13) declares.
func (s *Secrets) String(v string) string {
	out, _ := s.Bytes([]byte(v))
	return string(out)
}

// spanEnd returns the end of the redacted span that starts with v at b[i]. A
// variant that begins inside the span and ends past it extends the span, so
// overlapping secrets are redacted as one and no tail leaks. It reports false
// when a position inside the span lacks a full window of lookahead and more
// data may still arrive.
func (s *Secrets) spanEnd(b []byte, i int, v *variant, eof bool) (int, bool) {
	e := i + len(v.text)
	for j := i + 1; j < e; j++ {
		if len(s.byFirst[b[j]]) == 0 {
			continue
		}
		if !eof && j+s.maxLen > len(b) {
			return 0, false
		}
		if w := s.matchAt(b, j); w != nil && j+len(w.text) > e {
			e = j + len(w.text)
		}
	}
	return e, true
}
