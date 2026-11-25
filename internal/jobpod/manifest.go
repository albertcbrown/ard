package jobpod

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/gitref"
)

// ManifestVersion is the only manifest version this package reads.
const ManifestVersion = 1

// MaxManifestBytes caps the encoded manifest. The cap is on the JSON, because
// base64 grows inputs by a third and a Secret is limited to 1 MiB.
const MaxManifestBytes = 768 << 10

// ErrTooLarge means the encoded manifest is over MaxManifestBytes.
var ErrTooLarge = errors.New("jobpod: manifest exceeds the size cap")

// Source names a tree at a commit.
type Source struct {
	Repo string // owner/name
	SHA  string // 40-hex
	Path string // directory within the repo; unused for sidecars
}

// Manifest tells the pre container what to fetch and place.
type Manifest struct {
	V        int
	Repo     string // owner/name
	BaseSHA  string
	History  bool
	ReadOnly bool
	Role     Source  // role dir at the config commit
	Sidecar  *Source // pinned SHA
	Scrub    []string
	Inputs   []executor.File
}

// Encode validates and encodes m, refusing more than MaxManifestBytes.
func Encode(m Manifest) ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrTooLarge, len(b), MaxManifestBytes)
	}
	return b, nil
}

// Decode parses and validates a manifest. Unknown fields are refused.
func Decode(b []byte) (Manifest, error) {
	var m Manifest
	if len(b) > MaxManifestBytes {
		return m, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("jobpod: manifest: %w", err)
	}
	if err := m.validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func (m Manifest) validate() error {
	if m.V != ManifestVersion {
		return fmt.Errorf("jobpod: manifest version %d, want %d", m.V, ManifestVersion)
	}
	if !validRepo(m.Repo) {
		return fmt.Errorf("jobpod: bad repo %q", m.Repo)
	}
	if !gitref.IsSHA(m.BaseSHA) {
		return errors.New("jobpod: base sha is not a 40-hex commit")
	}
	if err := m.Role.validate("role", true); err != nil {
		return err
	}
	if m.Sidecar != nil {
		if err := m.Sidecar.validate("sidecar", false); err != nil {
			return err
		}
	}
	for _, p := range m.Scrub {
		if !cleanRel(p) {
			return fmt.Errorf("jobpod: scrub path %q is not a clean relative path", p)
		}
	}
	for _, f := range m.Inputs {
		if !cleanRel(f.Path) {
			return fmt.Errorf("jobpod: input path %q is not a clean relative path", f.Path)
		}
	}
	return nil
}

func (s Source) validate(what string, needPath bool) error {
	if !validRepo(s.Repo) {
		return fmt.Errorf("jobpod: %s: bad repo %q", what, s.Repo)
	}
	if !gitref.IsSHA(s.SHA) {
		return fmt.Errorf("jobpod: %s: sha is not a 40-hex commit", what)
	}
	if needPath && !cleanRel(s.Path) {
		return fmt.Errorf("jobpod: %s: path %q is not a clean relative path", what, s.Path)
	}
	return nil
}

func validRepo(r string) bool {
	o, n, ok := strings.Cut(r, "/")
	return ok && o != "" && n != "" && !strings.Contains(n, "/") &&
		!strings.ContainsAny(r, " \t\n\\:") && o != ".." && n != ".." && o != "." && n != "."
}

// cleanRel reports whether p is a non-empty, relative, already-clean path.
func cleanRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return false
	}
	return path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}
