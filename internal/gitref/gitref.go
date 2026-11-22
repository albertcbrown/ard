// Package gitref validates the ref names ard passes to git on a user's
// behalf. A ref from config is spliced into a refspec, so a ":" or a leading
// "-" must never get through. It is a leaf package so config and executor
// apply the same rule.
package gitref

import (
	"errors"
	"fmt"
	"strings"
)

const maxLen = 255

// IsSHA reports whether ref is a full 40-character lowercase hex commit ID.
func IsSHA(ref string) bool { return len(ref) == 40 && isHex(ref, false) }

func isHex(s string, upper bool) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		case upper && c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return s != ""
}

// Check returns an error describing why ref cannot be used as a fetch source.
func Check(ref string) error {
	switch {
	case ref == "":
		return errors.New("must not be empty")
	case len(ref) > maxLen:
		return fmt.Errorf("longer than %d bytes", maxLen)
	case ref == "@":
		return errors.New(`must not be "@"`)
	case strings.HasPrefix(ref, "-"):
		return errors.New(`must not start with "-"`)
	case strings.HasPrefix(ref, "/"), strings.HasSuffix(ref, "/"):
		return errors.New(`must not start or end with "/"`)
	case strings.HasSuffix(ref, "."):
		return errors.New(`must not end with "."`)
	case strings.HasSuffix(ref, ".lock"):
		return errors.New(`must not end with ".lock"`)
	case strings.Contains(ref, ".."):
		return errors.New(`must not contain ".."`)
	case strings.Contains(ref, "//"):
		return errors.New(`must not contain "//"`)
	case strings.Contains(ref, "@{"):
		return errors.New(`must not contain "@{"`)
	}
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte(` :?*[\^~`, c) >= 0 {
			return fmt.Errorf("must not contain %q", string(c))
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") {
			return errors.New(`a path component must not start with "."`)
		}
	}
	if len(ref) == 40 && isHex(ref, true) && !IsSHA(ref) {
		return errors.New("use a lowercase SHA")
	}
	if n := len(ref); n >= 7 && n < 40 && isHex(ref, true) {
		return errors.New("abbreviated SHA; use the full 40-character SHA")
	}
	return nil
}
