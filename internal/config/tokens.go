package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// TokenCount is a token amount that unmarshals from a plain integer or a
// string such as "40M", "1.5M" or "500k" (see ParseTokenCount).
type TokenCount int64

var tokenCountRE = regexp.MustCompile(`^([0-9]+)(?:\.([0-9]+))?([kKmM]?)$`)

// ParseTokenCount parses a positive whole number of tokens: digits with an
// optional decimal part and an optional k (x1000) or m (x1,000,000) suffix,
// either case. It uses integer arithmetic, so "1.5M" is exactly 1500000.
// Every error names the offending string.
func ParseTokenCount(s string) (int64, error) {
	m := tokenCountRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid token count %q: expected a number like %q, %q or %q", s, "40M", "1.5M", "500k")
	}
	intPart, frac, suffix := m[1], m[2], m[3]
	var mult int64 = 1
	switch strings.ToLower(suffix) {
	case "k":
		mult = 1_000
	case "m":
		mult = 1_000_000
	}
	if frac != "" && mult == 1 {
		return 0, fmt.Errorf("invalid token count %q: a fractional value needs a k or M suffix", s)
	}
	digits := strings.TrimLeft(intPart+frac, "0")
	if digits == "" {
		return 0, fmt.Errorf("invalid token count %q: must be greater than zero", s)
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid token count %q: too large", s)
	}
	if len(frac) > 7 {
		return 0, fmt.Errorf("invalid token count %q: too many decimals", s)
	}
	div := int64(math.Pow10(len(frac)))
	if n > math.MaxInt64/mult {
		return 0, fmt.Errorf("invalid token count %q: too large", s)
	}
	total := n * mult
	if total%div != 0 {
		return 0, fmt.Errorf("invalid token count %q: not a whole number of tokens", s)
	}
	return total / div, nil
}

// String renders t as a plain integer.
func (t TokenCount) String() string { return strconv.FormatInt(int64(t), 10) }

// UnmarshalYAML accepts a YAML integer or a string.
func (t *TokenCount) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected a token count like %q, got %s", "40M", nodeKindName(value))
	}
	switch value.Tag {
	case "!!null":
		*t = 0
		return nil
	case "!!int", "!!str":
		n, err := ParseTokenCount(value.Value)
		if err != nil {
			return err
		}
		*t = TokenCount(n)
		return nil
	default:
		return fmt.Errorf("expected a token count like %q, got %q", "40M", value.Value)
	}
}

// MarshalYAML renders t as an integer.
func (t TokenCount) MarshalYAML() (any, error) { return int64(t), nil }
