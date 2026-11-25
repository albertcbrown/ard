package jobpod

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/executor"
)

var labelRE = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?)?$`)

func TestPodName(t *testing.T) {
	id := strings.Repeat("A", MaxJobID)
	n := PodName(id)
	if len(n) > 63 || n != strings.ToLower(n) {
		t.Errorf("PodName = %q", n)
	}
}

func TestLabels(t *testing.T) {
	for _, in := range []string{strings.Repeat("role-", 20), "a b/c", "--x--", "ok", ""} {
		v := LabelValue(in)
		if len(v) > 63 || !labelRE.MatchString(v) {
			t.Errorf("LabelValue(%q) = %q", in, v)
		}
	}
	if LabelValue("ok") != "ok" {
		t.Error("valid value changed")
	}
	if r := RepoLabel("Owner/Repo"); len(r) != 16 || !labelRE.MatchString(r) || r != RepoLabel("owner/repo") {
		t.Errorf("RepoLabel = %q", r)
	}
}

func TestItemsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range append([]string{ItemManifest, ItemGitAccess, ItemResultKey}, ModelEnv...) {
		if seen[n] {
			t.Errorf("duplicate item %q", n)
		}
		seen[n] = true
	}
}

const sha = "0123456789abcdef0123456789abcdef01234567"

func good() Manifest {
	return Manifest{
		V: ManifestVersion, Repo: "o/r", BaseSHA: sha, History: true,
		Role:    Source{Repo: "o/r", SHA: sha, Path: ".ard/roles/x"},
		Sidecar: &Source{Repo: "o/side", SHA: sha},
		Scrub:   []string{"a/b"},
		Inputs:  []executor.File{{Path: ".ard-input/1/issue.md", Data: []byte("hi")}},
	}
}

func TestManifestRoundTrip(t *testing.T) {
	b, err := Encode(good())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if m.Repo != "o/r" || m.Sidecar == nil || string(m.Inputs[0].Data) != "hi" || !m.History {
		t.Errorf("m = %+v", m)
	}
}

func TestManifestCap(t *testing.T) {
	m := good()
	m.Inputs = []executor.File{{Path: "x"}}
	base, err := Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	// Base64 makes the encoded size depend on the data, so search for the
	// largest data length that fits, then check the next one does not.
	fits := func(n int) error {
		m.Inputs[0].Data = make([]byte, n)
		_, err := Encode(m)
		return err
	}
	lo, hi := 0, MaxManifestBytes
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(mid) == nil {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo <= len(base) {
		t.Fatal("search failed")
	}
	if err := fits(lo); err != nil {
		t.Errorf("boundary: %v", err)
	}
	if err := fits(lo + 1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("cap+1: err = %v", err)
	}
	if len(base) >= MaxManifestBytes {
		t.Error("base too big")
	}
}

func TestManifestRefusals(t *testing.T) {
	tests := map[string]func(*Manifest){
		"bad sha":       func(m *Manifest) { m.BaseSHA = "main" },
		"bad role sha":  func(m *Manifest) { m.Role.SHA = "abc" },
		"bad sidecar":   func(m *Manifest) { m.Sidecar.SHA = "abc" },
		"dotdot input":  func(m *Manifest) { m.Inputs[0].Path = "../x" },
		"abs input":     func(m *Manifest) { m.Inputs[0].Path = "/x" },
		"abs scrub":     func(m *Manifest) { m.Scrub = []string{"/etc/passwd"} },
		"unclean scrub": func(m *Manifest) { m.Scrub = []string{"a/../b"} },
		"unknown V":     func(m *Manifest) { m.V = 2 },
		"bad repo":      func(m *Manifest) { m.Repo = "norepo" },
		"role path":     func(m *Manifest) { m.Role.Path = "" },
	}
	for name, mut := range tests {
		m := good()
		mut(&m)
		if _, err := Encode(m); err == nil {
			t.Errorf("%s: Encode accepted", name)
		}
	}
	if _, err := Decode([]byte(`{"V":1,"Bogus":1}`)); err == nil {
		t.Error("unknown field accepted")
	}
	if _, err := Decode([]byte(`{"V":2}`)); err == nil {
		t.Error("unknown V decoded")
	}
}

func TestKeyMatches(t *testing.T) {
	k1, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := NewKey()
	if len(k1) != 43 || k1 == k2 {
		t.Errorf("keys %q %q", k1, k2)
	}
	if !KeyMatches(k1, k1) || KeyMatches(k1, k2) || KeyMatches("", k1) || KeyMatches(k1, "") {
		t.Error("KeyMatches wrong")
	}
}

func TestSecretData(t *testing.T) {
	d, err := SecretData([]byte("m"), "t", "k", "CLAUDE_CODE_OAUTH_TOKEN", "v")
	if err != nil || string(d["CLAUDE_CODE_OAUTH_TOKEN"]) != "v" || string(d[ItemManifest]) != "m" {
		t.Errorf("d = %v, %v", d, err)
	}
	if _, err := SecretData(nil, "t", "k", "PATH", "v"); err == nil {
		t.Error("unknown model name accepted")
	}
}
