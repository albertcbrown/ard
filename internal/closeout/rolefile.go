package closeout

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Stage words of the jobs close-out handles.
const (
	StageTriage = "triage"
	StageWorker = "worker"
	StageReview = "review"
)

// Role file statuses and reviewer verdicts.
const (
	StatusDone       = "done"
	StatusBlocked    = "blocked"
	StatusNeedsHuman = "needs-human"
	VerdictApprove   = "approve"
	VerdictChanges   = "changes"
)

// Finding severities, most serious first.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
	SeverityInfo     = "info"
)

// MaxFindings bounds the findings of one review; MaxFindingPath bounds a
// finding's file path in bytes.
const (
	MaxFindings    = 50
	MaxFindingPath = 300
)

// MaxFetched bounds the fetched inputs of one worker role file; MaxFetchedURL
// bounds a fetched URL in bytes.
const (
	MaxFetched    = 50
	MaxFetchedURL = 2000
)

// MaxRoleFileBytes bounds a role file.
const MaxRoleFileBytes = 256 << 10

var (
	// ErrMissing means the role file does not exist.
	ErrMissing = errors.New("closeout: role file missing")
	// ErrMalformed means the role file exists but breaks the contract. It is
	// always wrapped with the reason.
	ErrMalformed = errors.New("closeout: malformed role file")
)

// RoleFile is the parsed .ard/<issue#>/<stage>-<role>.md.
type RoleFile struct {
	Role, Status, Verdict, Worker string
	Attempt                       int
	Next                          []string // raw entries; see next.go
	Summary                       string
	// Findings are a review's findings, in file order. Only a review may have
	// them (check).
	Findings []Finding
	// Fetched are the network inputs a worker records, in file order. Only a
	// worker may have them (check).
	Fetched []Fetched
}

// Fetched is one input a worker fetched from the network.
type Fetched struct {
	URL        string // https only
	Date       string // YYYY-MM-DD
	Status     int    // HTTP status, 100..599
	SHA256     string // 64 lowercase hex
	VerifiedBy string // an https URL, or "none"
	Path       string // repo-relative landing path
}

// Finding is one point a reviewer raised.
type Finding struct {
	File     string // repo-relative slash path
	Line     int    // 1-based; 0 means the whole file
	Severity string
	Blocking bool
	Message  string // untrusted agent text
}

// HasBlocking reports whether any finding blocks.
func HasBlocking(fs []Finding) bool {
	for _, f := range fs {
		if f.Blocking {
			return true
		}
	}
	return false
}

// DowngradeVerdict makes an approve that carries a blocking finding a
// changes verdict, and reports whether it did. A reviewer that blocks and
// approves in one breath is read conservatively; the job still succeeds.
func (rf *RoleFile) DowngradeVerdict() bool {
	if rf.Verdict == VerdictApprove && HasBlocking(rf.Findings) {
		rf.Verdict = VerdictChanges
		return true
	}
	return false
}

type rawFinding struct {
	File     *string `yaml:"file"`
	Line     *int    `yaml:"line"`
	Severity *string `yaml:"severity"`
	Blocking *bool   `yaml:"blocking"`
	Message  *string `yaml:"message"`
}

type rawFetched struct {
	URL        *string `yaml:"url"`
	Date       *string `yaml:"date"`
	Status     *int    `yaml:"status"`
	SHA256     *string `yaml:"sha256"`
	VerifiedBy *string `yaml:"verified_by"`
	Path       *string `yaml:"path"`
}

type rawFront struct {
	Role    *string   `yaml:"role"`
	Status  *string   `yaml:"status"`
	Attempt *int      `yaml:"attempt"`
	Next    *[]string `yaml:"next"`
	Verdict *string   `yaml:"verdict"`
	Worker  *string   `yaml:"worker"`

	Findings *[]rawFinding `yaml:"findings"`
	Fetched  *[]rawFetched `yaml:"fetched"`
}

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, a...))
}

// Parse reads a role file: a first line of exactly "---", YAML, a line of
// exactly "---", then the summary. It checks the syntax and the field
// types; Check validates the fields against the job.
func Parse(b []byte) (RoleFile, error) {
	if len(b) > MaxRoleFileBytes {
		return RoleFile{}, malformed("file is %d bytes, max %d", len(b), MaxRoleFileBytes)
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return RoleFile{}, malformed("not valid UTF-8 text")
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	lines := strings.SplitAfter(string(b), "\n")
	fence := func(l string) bool { return strings.TrimSuffix(strings.TrimSuffix(l, "\n"), "\r") == "---" }
	if len(lines) == 0 || !fence(lines[0]) {
		return RoleFile{}, malformed("no opening --- line")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if fence(lines[i]) {
			end = i
			break
		}
	}
	if end < 0 {
		return RoleFile{}, malformed("no closing --- line")
	}
	front := strings.Join(lines[1:end], "")
	summary := strings.TrimSpace(strings.Join(lines[end+1:], ""))

	var raw rawFront
	dec := yaml.NewDecoder(strings.NewReader(front))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return RoleFile{}, malformed("empty frontmatter")
		}
		return RoleFile{}, malformed("frontmatter: %v", err)
	}
	if err := checkTypes(front); err != nil {
		return RoleFile{}, err
	}
	var extra rawFront
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return RoleFile{}, malformed("frontmatter holds more than one document")
	}
	rf := RoleFile{Summary: summary}
	var err error
	switch {
	case raw.Role == nil:
		return RoleFile{}, malformed("missing role")
	case raw.Status == nil:
		return RoleFile{}, malformed("missing status")
	case raw.Attempt == nil:
		return RoleFile{}, malformed("missing attempt")
	}
	rf.Role, rf.Status, rf.Attempt = *raw.Role, *raw.Status, *raw.Attempt
	if raw.Verdict != nil {
		rf.Verdict = *raw.Verdict
	}
	if raw.Worker != nil {
		rf.Worker = *raw.Worker
	}
	if raw.Next != nil {
		rf.Next = *raw.Next
	}
	if raw.Findings != nil {
		if rf.Findings, err = findingsOf(*raw.Findings); err != nil {
			return RoleFile{}, err
		}
	}
	if raw.Fetched != nil {
		if rf.Fetched, err = fetchedOf(*raw.Fetched); err != nil {
			return RoleFile{}, err
		}
	}
	switch rf.Status {
	case StatusDone, StatusBlocked, StatusNeedsHuman:
	default:
		return RoleFile{}, malformed("status %q is not done, blocked or needs-human", clip(rf.Status, 40))
	}
	if rf.Attempt < 1 {
		return RoleFile{}, malformed("attempt %d is below 1", rf.Attempt)
	}
	if rf.Verdict != "" && rf.Verdict != VerdictApprove && rf.Verdict != VerdictChanges {
		return RoleFile{}, malformed("verdict %q is not approve or changes", clip(rf.Verdict, 40))
	}
	if rf.Summary == "" {
		return RoleFile{}, malformed("empty summary")
	}
	return rf, nil
}

// check applies the rules that depend on the job: the role name and where a
// verdict or worker field may appear.
func (rf RoleFile) check(j Job) error {
	if rf.Role != j.Role {
		return malformed("role %q does not match job role %q", clip(rf.Role, 40), j.Role)
	}
	if rf.Verdict != "" && j.Stage != StageReview {
		return malformed("verdict is allowed only on a review")
	}
	if j.Stage == StageReview && rf.Status == StatusDone && rf.Verdict == "" {
		return malformed("a done review needs verdict: approve or changes")
	}
	if len(rf.Findings) > 0 && j.Stage != StageReview {
		return malformed("findings are allowed only on a review")
	}
	if len(rf.Fetched) > 0 && j.Stage != StageWorker {
		return malformed("fetched is allowed only on a worker")
	}
	if rf.Worker != "" && !(j.Stage == StageTriage && j.Role == "lead") {
		return malformed("worker is allowed only for triage:lead")
	}
	return nil
}

// findingsOf validates the findings of a review.
func findingsOf(raw []rawFinding) ([]Finding, error) {
	if len(raw) > MaxFindings {
		return nil, malformed("%d findings, max %d", len(raw), MaxFindings)
	}
	out := make([]Finding, 0, len(raw))
	for i, r := range raw {
		switch {
		case r.File == nil:
			return nil, malformed("finding %d: missing file", i+1)
		case r.Severity == nil:
			return nil, malformed("finding %d: missing severity", i+1)
		case r.Message == nil:
			return nil, malformed("finding %d: missing message", i+1)
		}
		f := Finding{File: *r.File, Severity: *r.Severity, Message: strings.TrimSpace(*r.Message)}
		if err := checkFindingPath(f.File); err != nil {
			return nil, malformed("finding %d: file %v", i+1, err)
		}
		switch f.Severity {
		case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo:
		default:
			return nil, malformed("finding %d: severity %q is not critical, high, medium, low or info", i+1, clip(f.Severity, 40))
		}
		if f.Message == "" {
			return nil, malformed("finding %d: empty message", i+1)
		}
		if r.Line != nil {
			if *r.Line < 1 {
				return nil, malformed("finding %d: line %d is below 1", i+1, *r.Line)
			}
			f.Line = *r.Line
		}
		f.Blocking = r.Blocking != nil && *r.Blocking
		out = append(out, f)
	}
	return out, nil
}

// fetchedOf validates the fetched inputs of a worker.
func fetchedOf(raw []rawFetched) ([]Fetched, error) {
	if len(raw) > MaxFetched {
		return nil, malformed("%d fetched entries, max %d", len(raw), MaxFetched)
	}
	out := make([]Fetched, 0, len(raw))
	for i, r := range raw {
		n := i + 1
		switch {
		case r.URL == nil:
			return nil, malformed("fetched %d: missing url", n)
		case r.Date == nil:
			return nil, malformed("fetched %d: missing date", n)
		case r.Status == nil:
			return nil, malformed("fetched %d: missing status", n)
		case r.SHA256 == nil:
			return nil, malformed("fetched %d: missing sha256", n)
		case r.VerifiedBy == nil:
			return nil, malformed("fetched %d: missing verified_by", n)
		case r.Path == nil:
			return nil, malformed("fetched %d: missing path", n)
		}
		f := Fetched{URL: *r.URL, Date: *r.Date, Status: *r.Status, SHA256: *r.SHA256, VerifiedBy: *r.VerifiedBy, Path: *r.Path}
		if err := checkHTTPS(f.URL); err != nil {
			return nil, malformed("fetched %d: url %v", n, err)
		}
		if _, err := time.Parse("2006-01-02", f.Date); err != nil {
			return nil, malformed("fetched %d: date %q is not YYYY-MM-DD", n, clip(f.Date, 40))
		}
		if f.Status < 100 || f.Status > 599 {
			return nil, malformed("fetched %d: status %d is not 100..599", n, f.Status)
		}
		if !isSHA256(f.SHA256) {
			return nil, malformed("fetched %d: sha256 is not 64 lowercase hex characters", n)
		}
		if f.VerifiedBy != "none" {
			if err := checkHTTPS(f.VerifiedBy); err != nil {
				return nil, malformed("fetched %d: verified_by %v", n, err)
			}
		}
		if err := checkFindingPath(f.Path); err != nil {
			return nil, malformed("fetched %d: path %v", n, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// checkHTTPS accepts a plain https URL with a host and no userinfo, and no
// character that could break out of a markdown code span.
func checkHTTPS(s string) error {
	if s == "" {
		return errors.New("is empty")
	}
	if len(s) > MaxFetchedURL {
		return fmt.Errorf("is %d bytes, max %d", len(s), MaxFetchedURL)
	}
	for _, c := range s {
		if c <= ' ' || c == 0x7f || c == '`' || c == '<' || c == '>' || c == '\\' {
			return errors.New("has a space, control or forbidden character")
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" {
		return errors.New("is not an https URL")
	}
	if u.Host == "" || u.Hostname() == "" {
		return errors.New("has no host")
	}
	if u.User != nil {
		return errors.New("has userinfo")
	}
	return nil
}

// checkFindingPath accepts only a clean repo-relative slash path.
func checkFindingPath(p string) error {
	switch {
	case p == "":
		return errors.New("is empty")
	case len(p) > MaxFindingPath:
		return fmt.Errorf("is %d bytes, max %d", len(p), MaxFindingPath)
	case strings.ContainsAny(p, "\\\x00"):
		return errors.New("has a backslash or NUL")
	case strings.HasPrefix(p, "/"):
		return errors.New("is absolute")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errors.New("is not a clean relative path")
		}
	}
	return nil
}

// readRoleFile reads path without following a symlink.
func readRoleFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, fmt.Errorf("stat role file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, malformed("not a regular file")
	}
	if fi.Size() > MaxRoleFileBytes {
		return nil, malformed("file is %d bytes, max %d", fi.Size(), MaxRoleFileBytes)
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, fmt.Errorf("read role file: %w", err)
	}
	return b, nil
}

// clip shortens untrusted text for a reason string.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// findingsTypes checks the scalar tags of a findings sequence.
func findingsTypes(v *yaml.Node) bool {
	if v.Kind != yaml.SequenceNode {
		return false
	}
	for _, e := range v.Content {
		if e.Kind != yaml.MappingNode {
			return false
		}
		for i := 0; i+1 < len(e.Content); i += 2 {
			val := e.Content[i+1]
			if val.Kind != yaml.ScalarNode {
				return false
			}
			switch e.Content[i].Value {
			case "file", "severity", "message":
				if val.Tag != "!!str" {
					return false
				}
			case "line":
				if val.Tag != "!!int" {
					return false
				}
			case "blocking":
				if val.Tag != "!!bool" {
					return false
				}
			}
		}
	}
	return true
}

// fetchedTypes checks the scalar tags of a fetched sequence.
func fetchedTypes(v *yaml.Node) bool {
	if v.Kind != yaml.SequenceNode {
		return false
	}
	for _, e := range v.Content {
		if e.Kind != yaml.MappingNode {
			return false
		}
		for i := 0; i+1 < len(e.Content); i += 2 {
			val := e.Content[i+1]
			if val.Kind != yaml.ScalarNode {
				return false
			}
			switch e.Content[i].Value {
			case "url", "sha256", "verified_by", "path":
				if val.Tag != "!!str" {
					return false
				}
			case "date":
				if val.Tag != "!!str" && val.Tag != "!!timestamp" {
					return false
				}
			case "status":
				if val.Tag != "!!int" {
					return false
				}
			}
		}
	}
	return true
}

// checkTypes rejects scalars yaml.v3 would coerce: role, status, verdict and
// worker must be strings, attempt an integer, next a list of strings.
func checkTypes(front string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(front), &doc); err != nil || len(doc.Content) == 0 {
		return malformed("frontmatter: unreadable")
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return malformed("frontmatter is not a mapping")
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, v := m.Content[i].Value, m.Content[i+1]
		ok := true
		switch key {
		case "role", "status", "verdict", "worker":
			ok = v.Kind == yaml.ScalarNode && v.Tag == "!!str"
		case "attempt":
			ok = v.Kind == yaml.ScalarNode && v.Tag == "!!int"
		case "findings":
			ok = findingsTypes(v)
		case "fetched":
			ok = fetchedTypes(v)
		case "next":
			ok = v.Kind == yaml.SequenceNode
			for _, e := range v.Content {
				if e.Kind != yaml.ScalarNode || e.Tag != "!!str" {
					ok = false
				}
			}
		}
		if !ok {
			return malformed("field %s: wrong type", key)
		}
	}
	return nil
}
