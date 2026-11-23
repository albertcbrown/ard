package redact

import (
	"bytes"
	"context"
	"fmt"
	"io"
)

type multi []Scanner

// Multi returns a Scanner that runs each scanner in order over the same
// bytes and concatenates their findings. The first error fails the scan and
// no findings are returned.
func Multi(scanners ...Scanner) Scanner { return multi(scanners) }

func (m multi) Scan(ctx context.Context, r io.Reader) ([]Finding, error) {
	if len(m) == 0 {
		return nil, nil
	}
	if len(m) == 1 {
		return m[0].Scan(ctx, r)
	}
	// Each scanner needs the whole input: rewind a seekable reader, else
	// hold the bytes in memory.
	var open func() (io.Reader, error)
	if rs, ok := r.(io.ReadSeeker); ok {
		open = func() (io.Reader, error) { _, err := rs.Seek(0, io.SeekStart); return rs, err }
	} else {
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		open = func() (io.Reader, error) { return bytes.NewReader(data), nil }
	}
	var out []Finding
	for _, s := range m {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		in, err := open()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		f, err := s.Scan(ctx, in)
		if err != nil {
			return nil, err
		}
		out = append(out, f...)
	}
	return out, nil
}
