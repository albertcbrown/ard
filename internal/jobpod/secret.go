package jobpod

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"slices"
)

// NewKey returns a fresh 32-byte result key as raw base64url.
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("jobpod: key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// SecretData builds the per-job Secret's items. modelName must be one of
// ModelEnv.
func SecretData(manifest []byte, gitToken, resultKey, modelName, modelValue string) (map[string][]byte, error) {
	if !slices.Contains(ModelEnv, modelName) {
		return nil, fmt.Errorf("jobpod: %q is not an allowed model credential name", modelName)
	}
	return map[string][]byte{
		ItemManifest:  manifest,
		ItemGitAccess: []byte(gitToken),
		ItemResultKey: []byte(resultKey),
		modelName:     []byte(modelValue),
	}, nil
}

// KeyMatches compares keys in constant time, whatever their lengths.
func KeyMatches(got, want string) bool {
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
