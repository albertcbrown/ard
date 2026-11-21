package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that unmarshals from YAML strings such as
// "15m", "4h", "30d" or "1w" (see ParseDuration) and marshals back to the
// same compact form.
type Duration time.Duration

// durationToken matches one number-plus-unit component of a duration.
var durationToken = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h|d|w)`)

// ParseDuration parses time.ParseDuration syntax extended with d (24 h) and
// w (7 d), including compound values ("1h30m", "7d"). It rejects empty
// strings, bare numbers ("30"), negatives, zero, fractional d/w values and
// anything under one second; every error names the offending string.
func ParseDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("invalid duration %q: empty", s)
	}
	if strings.HasPrefix(s, "-") {
		return 0, fmt.Errorf("invalid duration %q: negative durations are not allowed", s)
	}
	rest := s
	var total time.Duration
	for rest != "" {
		m := durationToken.FindStringSubmatch(rest)
		if m == nil {
			return 0, fmt.Errorf("invalid duration %q: expected a duration like %q, %q or %q", s, "30m", "4h", "7d")
		}
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %v", s, err)
		}
		var unit time.Duration
		switch m[2] {
		case "ns":
			unit = time.Nanosecond
		case "us", "µs":
			unit = time.Microsecond
		case "ms":
			unit = time.Millisecond
		case "s":
			unit = time.Second
		case "m":
			unit = time.Minute
		case "h":
			unit = time.Hour
		case "d":
			if strings.Contains(m[1], ".") {
				return 0, fmt.Errorf("invalid duration %q: %q does not take a fractional value", s, m[2])
			}
			unit = 24 * time.Hour
		case "w":
			if strings.Contains(m[1], ".") {
				return 0, fmt.Errorf("invalid duration %q: %q does not take a fractional value", s, m[2])
			}
			unit = 7 * 24 * time.Hour
		}
		total += time.Duration(n * float64(unit))
		rest = rest[len(m[0]):]
	}
	if total == 0 {
		return 0, fmt.Errorf("invalid duration %q: must be greater than zero", s)
	}
	if total < time.Second {
		return 0, fmt.Errorf("invalid duration %q: must be at least 1s", s)
	}
	return total, nil
}

// String renders d in the compact unit form accepted by ParseDuration.
func (d Duration) String() string {
	t := time.Duration(d)
	switch {
	case t == 0:
		return "0s"
	case t%(7*24*time.Hour) == 0:
		return fmt.Sprintf("%dw", t/(7*24*time.Hour))
	case t%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", t/(24*time.Hour))
	case t%time.Hour == 0:
		return fmt.Sprintf("%dh", t/time.Hour)
	default:
		h := t / time.Hour
		rest := t % time.Hour
		if rest%time.Minute == 0 {
			if h > 0 {
				return fmt.Sprintf("%dh%dm", h, rest/time.Minute)
			}
			return fmt.Sprintf("%dm", rest/time.Minute)
		}
		if h > 0 {
			return fmt.Sprintf("%dh%s", h, rest.String())
		}
		if m := t / time.Minute; m > 0 {
			return fmt.Sprintf("%dm%s", m, (t % time.Minute).String())
		}
		return t.String()
	}
}

// UnmarshalYAML accepts only scalar strings; numbers, booleans and
// collections are rejected with a message naming what was found.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected a duration string like %q, got %s", "15m", nodeKindName(value))
	}
	switch value.Tag {
	case "!!null":
		*d = 0
		return nil
	case "!!str":
		parsed, err := ParseDuration(value.Value)
		if err != nil {
			return err
		}
		*d = Duration(parsed)
		return nil
	case "!!int":
		return fmt.Errorf("expected a duration string like %q, got integer %s", "15m", value.Value)
	case "!!float":
		return fmt.Errorf("expected a duration string like %q, got float %s", "15m", value.Value)
	case "!!bool":
		return fmt.Errorf("expected a duration string like %q, got boolean %s", "15m", value.Value)
	default:
		return fmt.Errorf("expected a duration string like %q, got a value of type %s", "15m", value.Tag)
	}
}

// MarshalYAML renders d as its compact string form.
func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func nodeKindName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a sequence"
	case yaml.AliasNode:
		return "an alias"
	default:
		return fmt.Sprintf("tag %s", n.Tag)
	}
}
