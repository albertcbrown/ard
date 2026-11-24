package redact

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
)

// Finding is one scanner hit. It never carries the matched text.
type Finding struct {
	Rule string // rule ID, e.g. "github-token"
	Line int    // 1-based line in the scanned stream
	Col  int    // 1-based byte column
}

// Scanner looks for secrets in a stream. Implementations must fail closed:
// return an error rather than a partial verdict.
type Scanner interface {
	Scan(ctx context.Context, r io.Reader) ([]Finding, error)
}

const (
	scanWindow     = 1 << 20
	scanOverlap    = 4 << 10
	maxScanReports = 10000
)

type builtin struct{}

// Builtin returns the in-repo pattern scanner. It stores at most 10000
// findings per scan.
func Builtin() Scanner { return builtin{} }

func (builtin) Scan(ctx context.Context, r io.Reader) ([]Finding, error) {
	var out []Finding
	type key struct {
		rule      string
		line, col int
	}
	seen := map[key]bool{}
	emit := func(f Finding) {
		k := key{f.Rule, f.Line, f.Col}
		if !seen[k] && len(out) < maxScanReports {
			seen[k] = true
			out = append(out, f)
		}
	}

	br := bufio.NewReaderSize(r, scanWindow)
	line, consumed, iter := 1, 0, 0
	var carry []byte
	for {
		iter++
		if iter%1000 == 0 || carry != nil {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("scan: %w", err)
			}
		}
		frag, err := br.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			return nil, fmt.Errorf("scan: read: %w", err)
		}
		long := err == bufio.ErrBufferFull
		data, base := frag, consumed
		if carry != nil {
			data = append(append([]byte(nil), carry...), frag...)
			base = consumed - len(carry)
		}
		if len(data) > 0 {
			scanLine(bytes.TrimRight(data, "\r\n"), line, base, emit)
		}
		consumed += len(frag)
		if long {
			from := len(data) - scanOverlap
			if from < 0 {
				from = 0
			}
			carry = append([]byte(nil), data[from:]...)
			continue
		}
		carry, consumed = nil, 0
		if err == io.EOF {
			return out, nil
		}
		line++
	}
}

func scanLine(data []byte, line, base int, emit func(Finding)) {
	for i := range rules {
		ru := &rules[i]
		if ru.generic {
			if bytes.IndexByte(data, ':') < 0 && bytes.IndexByte(data, '=') < 0 {
				continue
			}
			for _, m := range ru.re.FindAllSubmatchIndex(data, -1) {
				quoted := m[3] > m[2]
				v := data[m[4]:m[5]]
				if !quoted && (codeRef(v) || numeric(v)) {
					continue
				}
				if placeholder(v) || entropy(v) < entropyThreshold {
					continue
				}
				emit(Finding{ru.id, line, base + m[0] + 1})
			}
			continue
		}
		hit := false
		for _, k := range ru.kw {
			if bytes.Contains(data, k) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for _, m := range ru.re.FindAllIndex(data, -1) {
			emit(Finding{ru.id, line, base + m[0] + 1})
		}
	}
}
