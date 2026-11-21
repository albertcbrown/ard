package redact

import (
	"bytes"
	"fmt"
	"io"
)

const markerReserve = 160

// capReader returns the content of f (size bytes) capped to maxBytes as head
// plus tail, cut on line boundaries and joined by one marker line.
func capReader(f io.ReaderAt, size, maxBytes, headBytes int64) ([]byte, bool, error) {
	if size <= maxBytes {
		out := make([]byte, size)
		if _, err := f.ReadAt(out, 0); err != nil && !(err == io.EOF && size == 0) {
			if err != io.EOF {
				return nil, false, fmt.Errorf("cap: read: %w", err)
			}
		}
		return out, false, nil
	}
	head := make([]byte, headBytes)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, false, fmt.Errorf("cap: read head: %w", err)
	}
	if i := bytes.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	}
	tailBudget := maxBytes - int64(len(head)) - markerReserve
	start := size - tailBudget
	buf := make([]byte, tailBudget+1) // one byte of look-behind
	if _, err := f.ReadAt(buf, start-1); err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("cap: read tail: %w", err)
	}
	tail := buf[1:]
	if buf[0] != '\n' {
		if i := bytes.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:]
		}
	}
	omitted := size - int64(len(head)) - int64(len(tail))
	lead := ""
	if len(head) > 0 && head[len(head)-1] != '\n' {
		lead = "\n"
	}
	m := fmt.Sprintf("%s[ard: log truncated: %d bytes omitted; kept first %d and last %d of %d redacted bytes]\n",
		lead, omitted, len(head), len(tail), size)
	out := make([]byte, 0, len(head)+len(m)+len(tail))
	out = append(append(append(out, head...), m...), tail...)
	if int64(len(out)) > maxBytes {
		return nil, false, fmt.Errorf("cap: output %d bytes exceeds limit %d", len(out), maxBytes)
	}
	return out, true, nil
}
