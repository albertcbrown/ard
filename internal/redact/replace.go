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
	have := 0 // carried bytes at the start of buf; ≤ maxLen-1
	eof := false
	spanOpen := false // a redacted span (marker already written) continues into b
	spanRem := 0      // its end, as an offset into b, while spanOpen
	werr := func(err error) error { return fmt.Errorf("redact stream: write: %w", err) }
	for !eof {
		if err := ctx.Err(); err != nil {
			return nil, total, fmt.Errorf("redact stream: %w", err)
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
		last, flush := 0, -1
		i := 0
		if spanOpen { // resume the open span; its marker is already out
			end, wait, ok := s.spanEnd(b, 0, spanRem, eof)
			if !ok {
				spanRem, flush = end-wait, wait
			} else {
				spanOpen, i, last = false, end, end
			}
		}
		for ; flush < 0 && i < cut && s.maxLen > 0; i++ {
			if len(s.byFirst[b[i]]) == 0 {
				continue
			}
			v := s.matchAt(b, i)
			if v == nil {
				continue
			}
			end, wait, ok := s.spanEnd(b, i+1, i+len(v.text), eof)
			if _, err := w.Write(b[last:i]); err != nil {
				return nil, total, werr(err)
			}
			if _, err := io.WriteString(w, marker(v.name)); err != nil {
				return nil, total, werr(err)
			}
			counts[v.name]++
			if !ok {
				// Everything in b[i:wait] is inside the span: drop it and
				// carry on from wait with the span still open.
				spanOpen, spanRem, flush = true, end-wait, wait
				break
			}
			i, last = end-1, end
		}
		if flush < 0 {
			flush = last
			if last < cut {
				if _, err := w.Write(b[last:cut]); err != nil {
					return nil, total, werr(err)
				}
				flush = cut
			}
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

// spanEnd walks b[from:e], where e is the current end of a redacted span. A
// variant that begins inside the span and ends past it extends the span, so
// overlapping secrets are redacted as one and no tail leaks. It returns the
// final end. When a position lacks a full window of lookahead and more data
// may still arrive, it returns ok == false with the position to resume at.
func (s *Secrets) spanEnd(b []byte, from, e int, eof bool) (end, wait int, ok bool) {
	for j := from; j < e; j++ {
		if len(s.byFirst[b[j]]) == 0 {
			continue
		}
		if !eof && j+s.maxLen > len(b) {
			return e, j, false
		}
		if w := s.matchAt(b, j); w != nil && j+len(w.text) > e {
			e = j + len(w.text)
		}
	}
	return e, 0, true
}
