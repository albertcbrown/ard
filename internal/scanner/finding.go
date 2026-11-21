package scanner

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Severity of a finding.
type Severity string

// The five severities, highest first.
const (
	SevCritical Severity = "critical"
	SevHigh     Severity = "high"
	SevMedium   Severity = "medium"
	SevLow      Severity = "low"
	SevInfo     Severity = "info"
)

// Rank orders severities: critical is 5, info is 1, an unknown value is 0.
func (s Severity) Rank() int {
	switch s {
	case SevCritical:
		return 5
	case SevHigh:
		return 4
	case SevMedium:
		return 3
	case SevLow:
		return 2
	case SevInfo:
		return 1
	}
	return 0
}

// Valid reports whether s is one of the five severities.
func (s Severity) Valid() bool { return s.Rank() > 0 }

// Limits on scanner output. Any violation fails the whole run.
const (
	MaxLineBytes   = 1 << 20  // one JSONL line
	MaxStreamBytes = 16 << 20 // the whole stdout
	MaxFindings    = 10000
	MaxBodyBytes   = 64 << 10
	MaxFingerprint = 512 // bytes
	MaxTitleRunes  = 200
	MaxGroupBytes  = 256
	MaxFiles       = 1000
	maxPathBytes   = 4096
	maxErrorLines  = 20
)

// Finding is one line of scanner output.
type Finding struct {
	Fingerprint string   `json:"fingerprint"` // required; stable across runs
	Title       string   `json:"title"`       // required; one line
	Severity    Severity `json:"severity"`    // required
	Group       string   `json:"group"`       // optional; "" = one issue per finding
	Files       []string `json:"files"`       // optional; repo-relative
	Body        string   `json:"body"`        // optional markdown
}

// DecodeError is one invalid line of scanner output.
type DecodeError struct {
	Line int
	Msg  string
}

func (e DecodeError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// DecodeErrors lists the invalid lines of a scanner's output, at most 20.
type DecodeErrors []DecodeError

func (e DecodeErrors) Error() string {
	parts := make([]string, 0, len(e))
	for _, d := range e {
		parts = append(parts, d.Error())
	}
	return "invalid scanner output: " + strings.Join(parts, "; ")
}

// DecodeFindings parses scanner stdout as JSON lines, strictly: unknown keys,
// a missing required key, a bad value, a duplicate fingerprint or an
// oversized line or stream are errors. Blank lines are skipped. It never
// returns partial findings, so a half-parsed run can never trigger autoclose.
func DecodeFindings(r io.Reader) ([]Finding, error) {
	var (
		out  []Finding
		errs DecodeErrors
		seen = map[string]int{}
		n    int
		read int
	)
	fail := func(line int, format string, a ...any) {
		if len(errs) < maxErrorLines {
			errs = append(errs, DecodeError{Line: line, Msg: fmt.Sprintf(format, a...)})
		}
	}
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, tooLong, err := readLine(br, MaxLineBytes)
		read += len(line)
		if tooLong {
			fail(n+1, "longer than %d bytes", MaxLineBytes)
			return nil, errs
		}
		if len(line) > 0 || err == nil {
			n++
			if t := strings.TrimSpace(string(line)); t != "" {
				if len(out)+1 > MaxFindings {
					fail(n, "more than %d findings", MaxFindings)
					return nil, errs
				}
				f, derr := decodeLine([]byte(t))
				switch {
				case derr != nil:
					fail(n, "%v", derr)
				default:
					if first, dup := seen[f.Fingerprint]; dup {
						fail(n, "duplicate fingerprint %q (first on line %d)", f.Fingerprint, first)
					} else {
						seen[f.Fingerprint] = n
						out = append(out, f)
					}
				}
			}
		}
		if read > MaxStreamBytes {
			fail(n, "output larger than %d bytes", MaxStreamBytes)
			return nil, errs
		}
		if err != nil {
			if err != io.EOF {
				return nil, fmt.Errorf("read scanner output: %w", err)
			}
			break
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// readLine returns the next line without its newline. It stops reading a line
// longer than max and reports tooLong.
func readLine(br *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > max+1 {
			return nil, true, nil
		}
		switch e {
		case nil:
			return trimNL(line), false, nil
		case bufio.ErrBufferFull:
			continue
		default:
			return trimNL(line), false, e
		}
	}
}

func trimNL(b []byte) []byte {
	b = []byte(strings.TrimSuffix(string(b), "\n"))
	return []byte(strings.TrimSuffix(string(b), "\r"))
}

func decodeLine(b []byte) (Finding, error) {
	var f Finding
	if err := decodeStrict(b, &f); err != nil {
		return Finding{}, err
	}
	if err := validateFinding(f); err != nil {
		return Finding{}, err
	}
	return f, nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validateFinding(f Finding) error {
	switch {
	case f.Fingerprint == "":
		return fmt.Errorf("fingerprint is required")
	case len(f.Fingerprint) > MaxFingerprint:
		return fmt.Errorf("fingerprint longer than %d bytes", MaxFingerprint)
	case !utf8.ValidString(f.Fingerprint) || hasControl(f.Fingerprint):
		return fmt.Errorf("fingerprint %q is not valid UTF-8 without control characters", f.Fingerprint)
	case strings.TrimSpace(f.Title) == "":
		return fmt.Errorf("title is required")
	case utf8.RuneCountInString(f.Title) > MaxTitleRunes:
		return fmt.Errorf("title longer than %d characters", MaxTitleRunes)
	case !utf8.ValidString(f.Title) || hasControl(f.Title):
		return fmt.Errorf("title must be one line of valid UTF-8")
	case !f.Severity.Valid():
		return fmt.Errorf("severity %q is not one of critical, high, medium, low, info", f.Severity)
	case len(f.Group) > MaxGroupBytes || !utf8.ValidString(f.Group) || hasControl(f.Group):
		return fmt.Errorf("group must be at most %d bytes of one-line valid UTF-8", MaxGroupBytes)
	case len(f.Files) > MaxFiles:
		return fmt.Errorf("more than %d files", MaxFiles)
	case len(f.Body) > MaxBodyBytes:
		return fmt.Errorf("body longer than %d bytes", MaxBodyBytes)
	case !utf8.ValidString(f.Body):
		return fmt.Errorf("body is not valid UTF-8")
	}
	for _, p := range f.Files {
		if err := checkFilePath(p); err != nil {
			return err
		}
	}
	return nil
}

func checkFilePath(p string) error {
	switch {
	case p == "" || len(p) > maxPathBytes || !utf8.ValidString(p) || hasControl(p):
		return fmt.Errorf("file %q is not a valid path", p)
	case path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || p == ".":
		return fmt.Errorf("file %q must be a clean repo-relative path without ..", p)
	}
	return nil
}
