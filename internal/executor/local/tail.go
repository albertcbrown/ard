package local

import (
	"bytes"
	"io"
	"os"
)

// tailLines returns the last n lines of f (n > 0), reading backwards.
func tailLines(f *os.File, n int) ([]byte, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const chunk = 64 << 10
	pos := st.Size()
	var buf []byte
	for pos > 0 {
		sz := int64(chunk)
		if sz > pos {
			sz = pos
		}
		pos -= sz
		b := make([]byte, sz)
		if _, err := f.ReadAt(b, pos); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(b, buf...)
		// n lines need n newlines after trimming a trailing one, plus the
		// newline that precedes the first of them.
		if bytes.Count(bytes.TrimSuffix(buf, []byte("\n")), []byte("\n")) >= n {
			break
		}
	}
	trimmed := bytes.TrimSuffix(buf, []byte("\n"))
	lines := bytes.Split(trimmed, []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(buf) == 0 {
		return nil, nil
	}
	out := bytes.Join(lines, []byte("\n"))
	if bytes.HasSuffix(buf, []byte("\n")) {
		out = append(out, '\n')
	}
	return out, nil
}
