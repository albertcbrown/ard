package committer

import (
	"path"
	"strings"
)

// CheckCodePath reports why a worker may not commit p for issue, or nil. It is
// the one copy of the rule: the worker's diff guard (#33) and CommitCode both
// call it. Paths must already be canonical; they are rejected, not normalised.
//
// Refused: a .git segment, .gitmodules, everything under .ard/ (role, config
// and issue files; the issue's own go through close-out, not through worker
// code), everything under .ard-input/ (loop inputs), everything under the
// root .github/ (not only workflows), and secret-shaped file names. Only the
// repository-root .github/ is reserved; docs/.github/x is ordinary. The
// reserved first segments match case-insensitively because a case-insensitive
// filesystem would fold them.
//
// issue is accepted for the message and future use; it does not change the
// outcome today, because every .ard/ path is refused.
func CheckCodePath(issue int, p string) error {
	_ = issue
	if p == "" {
		return reason("empty path")
	}
	for _, r := range p {
		if r == '\\' || r < 0x20 || r == 0x7f {
			return reason("backslash or control character")
		}
	}
	if path.Clean(p) != p || strings.HasPrefix(p, "/") {
		return reason("not canonical")
	}
	segs := strings.Split(p, "/")
	for _, seg := range segs {
		if strings.EqualFold(seg, ".git") {
			return reason("contains .git")
		}
	}
	if len(segs) > 1 {
		switch strings.ToLower(segs[0]) {
		case ".ard":
			return reason("under .ard/ (role, config and issue files are not worker code)")
		case ".ard-input":
			return reason("under .ard-input/ (loop inputs)")
		case ".github":
			return reason("under .github/")
		}
	}
	// A file named like a reserved directory is refused too.
	switch strings.ToLower(segs[0]) {
	case ".ard", ".ard-input", ".github":
		if len(segs) == 1 {
			return reason("reserved name " + segs[0])
		}
	}
	if strings.EqualFold(segs[len(segs)-1], ".gitmodules") {
		return reason("submodule configuration")
	}
	if SecretShaped(p) {
		return reason("secret-shaped file name")
	}
	return nil
}

var (
	secretExact = map[string]bool{
		".env": true, ".netrc": true, "_netrc": true, ".npmrc": true, ".pypirc": true,
		".htpasswd": true, ".git-credentials": true, ".dockercfg": true,
		"credentials": true, "credentials.json": true,
	}
	secretEnvOK       = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true}
	secretKeyPrefixes = []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"}
	secretSuffixes    = []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk", ".kdbx"}
	secretPairs       = []string{".aws/credentials", ".docker/config.json"}
)

// SecretShaped reports whether p names a file that usually holds a credential.
func SecretShaped(p string) bool {
	p = strings.ToLower(p)
	base := path.Base(p)
	if secretExact[base] {
		return true
	}
	if strings.HasPrefix(base, ".env.") && !secretEnvOK[base] {
		return true
	}
	for _, pre := range secretKeyPrefixes {
		if strings.HasPrefix(base, pre) {
			return true
		}
	}
	if strings.HasPrefix(base, "service-account") && strings.HasSuffix(base, ".json") {
		return true
	}
	for _, suf := range secretSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	for _, pair := range secretPairs {
		if p == pair || strings.HasSuffix(p, "/"+pair) {
			return true
		}
	}
	return false
}
