package config

import "strings"

// Error is one configuration problem. File is the repo-relative file or
// directory it was found in (".ard/config.yaml" or ".ard/scanners/cve"),
// Key the dotted key path within it ("intervals.triage") and Msg the
// human-readable detail.
type Error struct {
	File string
	Key  string
	Msg  string
}

func (e *Error) Error() string {
	switch {
	case e.Key == "":
		return e.File + ": " + e.Msg
	default:
		return e.File + ": " + e.Key + ": " + e.Msg
	}
}

// Errors is the collection of every problem found while loading; loading
// reports all of them, not just the first.
type Errors []*Error

func (es Errors) Error() string {
	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

func (es Errors) Unwrap() []error {
	errs := make([]error, len(es))
	for i, e := range es {
		errs[i] = e
	}
	return errs
}
