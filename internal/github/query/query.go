// Package query builds GitHub issue-search queries for ard's request labels.
// It has no dependency on internal/github.
package query

import (
	"errors"
	"fmt"
	"strings"
)

// Type selects the is:issue / is:pr qualifier.
type Type string

const (
	TypeAny   Type = ""
	TypeIssue Type = "issue"
	TypePR    Type = "pr"
)

// RoleQuery describes "open items carrying Request but none of States or
// Guards". The result carries no repo: qualifier; SearchIssues adds it.
type RoleQuery struct {
	Type    Type
	Request string   // included label, always quoted
	States  []string // excluded role-state labels, always quoted
	Guards  []string // excluded static labels, bare unless they need quoting
	// AnyOf, when set, requires at least one of these labels (GitHub's
	// comma form, label:"a","b"). Always quoted.
	AnyOf []string
}

// Build renders the query string.
func (q RoleQuery) Build() (string, error) {
	if q.Request == "" {
		return "", errors.New("query: empty request label")
	}
	parts := []string{"is:open"}
	if q.Type != TypeAny {
		parts = append(parts, "is:"+string(q.Type))
	}
	if err := check(q.Request); err != nil {
		return "", err
	}
	parts = append(parts, `label:"`+q.Request+`"`)
	if len(q.AnyOf) > 0 {
		quoted := make([]string, len(q.AnyOf))
		for i, a := range q.AnyOf {
			if err := check(a); err != nil {
				return "", err
			}
			quoted[i] = `"` + a + `"`
		}
		parts = append(parts, "label:"+strings.Join(quoted, ","))
	}
	for _, s := range q.States {
		if err := check(s); err != nil {
			return "", err
		}
		parts = append(parts, `-label:"`+s+`"`)
	}
	for _, g := range q.Guards {
		if err := check(g); err != nil {
			return "", err
		}
		if needsQuote(g) {
			g = `"` + g + `"`
		}
		parts = append(parts, "-label:"+g)
	}
	return strings.Join(parts, " "), nil
}

func check(label string) error {
	if label == "" {
		return errors.New("query: empty label")
	}
	if strings.ContainsAny(label, "\"\n\r") {
		return fmt.Errorf("query: label %q contains a quote or newline", label)
	}
	return nil
}

func needsQuote(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(":._/-", r):
		default:
			return true
		}
	}
	return false
}
