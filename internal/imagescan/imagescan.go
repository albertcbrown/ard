// Package imagescan checks a container image's filesystem, read as the tar
// stream `docker export` writes, for credentials: files whose names say they
// hold one (SSH keys, .netrc, a docker or kube config, a claude credentials
// file, ...) and content that matches the redact package's secret rules. The
// published images (ard and ard-agent) must hold none; `make image-test` runs
// it on both and fails on any finding. It fails closed: an unreadable stream
// is an error, never a clean verdict.
package imagescan

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/redact"
)

// Finding is one hit. It never carries the matched text.
type Finding struct {
	Path string // path in the image, without a leading slash
	Rule string // "name:<what>" for a file-name hit, else the redact rule id
	Line int    // 1-based line of a content hit; 0 for a name hit
}

func (f Finding) String() string {
	if f.Line == 0 {
		return fmt.Sprintf("/%s: %s", f.Path, f.Rule)
	}
	return fmt.Sprintf("/%s:%d: %s", f.Path, f.Line, f.Rule)
}

// Allow exempts known public fixtures from the content scan. Every entry says
// why; name checks are never exempt.
type Allow struct {
	Glob   string // path.Match pattern on each path prefix, e.g. usr/local/go/src/*/testdata
	Reason string
}

// DefaultAllow is what the shipped images need: the Go distribution's own
// test fixtures (public keys and fake tokens in the standard library's
// testdata and tests, identical in every golang image).
var DefaultAllow = []Allow{
	{Glob: "usr/local/go/src/**/testdata/**", Reason: "Go standard library test fixtures"},
	{Glob: "usr/local/go/src/**/*_test.go", Reason: "Go standard library tests"},
	{Glob: "usr/local/go/pkg/**/testdata/**", Reason: "Go toolchain test fixtures"},
	{Glob: "usr/local/go/src/crypto/x509/platform_root_key.pem", Reason: "Go standard library test key (platform_test.go)"},
	{Glob: "usr/lib/*-linux-gnu/libgnutls.so.*", Reason: "GnuTLS self-test keys (public test vectors in the Debian library)"},
}

// contentRules are the redact rules the content scan reports. The generic
// assignment, JWT and bare sk- rules are tuned for logs; in an operating
// system's files they match documentation and code, so they are left out.
// redact's private-key rule matches a PEM header alone, which every TLS and
// SSH binary carries as a parser constant; privateKey below replaces it.
var contentRules = map[string]bool{
	"github-token": true, "github-fine-grained": true, "aws-access-key": true,
	"slack-token": true, "slack-webhook": true,
	"anthropic-key": true, "gcp-api-key": true, "stripe-live": true,
}

// privateKey is a PEM private key with a body: the header, up to four PEM
// headers (Proc-Type, DEK-Info), then a base64 line.
var privateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----\r?\n(?:[A-Za-z-]+: [^\n]{0,200}\r?\n){0,4}(?:\r?\n)?[A-Za-z0-9+/=]{40,}`)

// credName matches base names that hold a credential by convention.
var credName = []struct {
	what string
	re   *regexp.Regexp
}{
	{"ssh-private-key", regexp.MustCompile(`^(id_(rsa|dsa|ecdsa|ed25519)(_sk)?|ssh_host_.*_key|identity)$`)},
	{"netrc", regexp.MustCompile(`^[._]netrc$`)},
	{"git-credentials", regexp.MustCompile(`^\.git-credentials$`)},
	{"package-registry-config", regexp.MustCompile(`^\.(npmrc|pypirc|yarnrc\.yml|gem/credentials)$`)},
	{"key-store", regexp.MustCompile(`(?i)\.(p12|pfx|jks|keystore|ppk|kdbx)$`)},
	{"dotenv", regexp.MustCompile(`^\.env(\..+)?$`)},
	{"claude-credentials", regexp.MustCompile(`^\.credentials\.json$`)},
}

// credPath matches whole paths (any prefix) that hold a credential.
var credPath = []struct {
	what string
	re   *regexp.Regexp
}{
	{"docker-config", regexp.MustCompile(`(^|/)\.docker/config\.json$`)},
	{"kube-config", regexp.MustCompile(`(^|/)\.kube/config$`)},
	{"gh-hosts", regexp.MustCompile(`(^|/)gh/hosts\.yml$`)},
	{"cloud-credentials", regexp.MustCompile(`(^|/)\.(aws/credentials|config/gcloud/.+\.json|azure/.+)$`)},
	{"claude-config", regexp.MustCompile(`(^|/)\.claude\.json$|(^|/)\.claude/`)},
	{"ssh-dir", regexp.MustCompile(`(^|/)\.ssh/`)},
	{"gnupg-dir", regexp.MustCompile(`(^|/)\.gnupg/`)},
}

// Options tunes a scan. Zero means DefaultAllow and 512 MiB per file.
type Options struct {
	Allow   []Allow
	MaxFile int64
}

// Scan reads a tar stream and returns every finding, sorted. An error means
// the verdict is unknown.
func Scan(ctx context.Context, r io.Reader, o Options) ([]Finding, error) {
	if o.Allow == nil {
		o.Allow = DefaultAllow
	}
	if o.MaxFile == 0 {
		o.MaxFile = 512 << 20
	}
	sc := redact.Builtin()
	var out []Finding
	tr := tar.NewReader(r)
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("imagescan: read tar: %w", err)
		}
		n++
		p := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if h.Typeflag == tar.TypeDir {
			continue
		}
		out = append(out, nameFindings(p)...)
		if h.Typeflag != tar.TypeReg || allowed(p, o.Allow) {
			continue
		}
		if h.Size > o.MaxFile {
			return nil, fmt.Errorf("imagescan: /%s is %d bytes, over the %d-byte limit", p, h.Size, o.MaxFile)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("imagescan: read /%s: %w", p, err)
		}
		fs, err := sc.Scan(ctx, bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("imagescan: scan /%s: %w", p, err)
		}
		for _, f := range fs {
			if contentRules[f.Rule] {
				out = append(out, Finding{Path: p, Rule: f.Rule, Line: f.Line})
			}
		}
		for _, m := range privateKey.FindAllIndex(b, -1) {
			out = append(out, Finding{Path: p, Rule: "private-key", Line: 1 + bytes.Count(b[:m[0]], []byte("\n"))})
		}
	}
	if n == 0 {
		return nil, errors.New("imagescan: empty tar stream")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Rule < out[j].Rule
	})
	return out, nil
}

func nameFindings(p string) []Finding {
	var out []Finding
	base := path.Base(p)
	for _, c := range credName {
		if c.re.MatchString(base) {
			out = append(out, Finding{Path: p, Rule: "name:" + c.what})
		}
	}
	for _, c := range credPath {
		if c.re.MatchString(p) {
			out = append(out, Finding{Path: p, Rule: "name:" + c.what})
		}
	}
	return out
}

// allowed reports whether p matches an Allow glob. "**" matches any number
// of path segments, "*" one segment's characters.
func allowed(p string, allow []Allow) bool {
	for _, a := range allow {
		if globMatch(strings.Split(a.Glob, "/"), strings.Split(p, "/")) {
			return true
		}
	}
	return false
}

func globMatch(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return len(segs) > 0
			}
			for i := 0; i <= len(segs); i++ {
				if globMatch(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
