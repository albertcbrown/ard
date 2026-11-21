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
	for eof := false; !eof; {
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
		last := 0
		for i := 0; i < cut && s.maxLen > 0; {
			if len(s.byFirst[b[i]]) > 0 {
				if v := s.matchAt(b, i); v != nil {
					if _, err := w.Write(b[last:i]); err != nil {
						return nil, total, fmt.Errorf("redact stream: write: %w", err)
					}
					if _, err := io.WriteString(w, marker(v.name)); err != nil {
						return nil, total, fmt.Errorf("redact stream: write: %w", err)
					}
					counts[v.name]++
					i += len(v.text)
					last = i
					continue
				}
			}
			i++
		}
		flush := last
		if last < cut {
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
