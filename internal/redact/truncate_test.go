package redact

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCapReader(t *testing.T) {
	const max, head = 4096, 256
	lines := func(n int) string {
		var sb strings.Builder
		for i := 0; sb.Len() < n; i++ {
			fmt.Fprintf(&sb, "line %05d\n", i)
		}
		return sb.String()[:n]
	}
	tests := map[string]string{
		"empty":        "",
		"exactly max":  lines(max),
		"max plus one": lines(max + 1),
		"many lines":   lines(50000),
		"no newline":   strings.Repeat("x", 20000),
		"head no nl":   strings.Repeat("h", 400) + "\n" + lines(20000),
		"tail no nl":   lines(300) + strings.Repeat("t", 20000),
		"all newlines": strings.Repeat("\n", 10000),
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			out, trunc, err := capReader(strings.NewReader(in), int64(len(in)), max, head)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) > max {
				t.Fatalf("len = %d", len(out))
			}
			if trunc != (len(in) > max) {
				t.Fatalf("trunc = %v for len %d", trunc, len(in))
			}
			if !trunc {
				if string(out) != in {
					t.Fatal("untruncated output differs")
				}
				return
			}
			if n := strings.Count(string(out), "[ard: log truncated:"); n != 1 {
				t.Fatalf("markers = %d", n)
			}
			i := bytes.Index(out, []byte("[ard: log truncated:"))
			hd := out[:i]
			if !strings.HasPrefix(in, string(hd)) && !(i > 0 && hd[i-1] == '\n' && strings.HasPrefix(in, string(hd[:i-1]))) {
				t.Errorf("head is not a prefix of the input")
			}
			if i > head+1 {
				t.Errorf("head too long: %d", i)
			}
			tail := out[i+bytes.IndexByte(out[i:], '\n')+1:]
			if !strings.HasSuffix(in, string(tail)) {
				t.Fatalf("tail is not a suffix of the input")
			}
			if n := len(in) - len(tail); len(tail) > 0 && n > 0 && in[n-1] != '\n' && bytes.IndexByte(tail, '\n') >= 0 {
				t.Errorf("tail starts mid-line")
			}
		})
	}
}
