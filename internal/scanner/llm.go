package scanner

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	llmRuleRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	llmPathRe   = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)
	llmSymbolRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./-]{0,127}$`)
)

// ParseLLMFingerprint splits an LLM scanner fingerprint, "rule:path" or
// "rule:path:symbol", and reports whether it is well formed. The path must
// be a clean, relative, repo-style path without ".." segments.
func ParseLLMFingerprint(fp string) (rule, p, symbol string, ok bool) {
	if len(fp) > MaxFingerprint {
		return "", "", "", false
	}
	parts := strings.Split(fp, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", "", false
	}
	rule, p = parts[0], parts[1]
	if len(parts) == 3 {
		symbol = parts[2]
		if !llmSymbolRe.MatchString(symbol) {
			return "", "", "", false
		}
	}
	if !llmRuleRe.MatchString(rule) || !llmPathRe.MatchString(p) {
		return "", "", "", false
	}
	if p == "." || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return "", "", "", false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", "", "", false
		}
	}
	return rule, p, symbol, true
}

// CheckLLMFingerprints validates every finding's fingerprint against the LLM
// grammar, requires its path to be listed in the finding's Files and, via
// exists, to be present in the repository. A nil exists treats every path as
// present. Errors carry the 1-based finding index and fixed text only, never
// the fingerprint or any finding text; at most 20 are returned.
func CheckLLMFingerprints(fs []Finding, exists func(path string) bool) DecodeErrors {
	var errs DecodeErrors
	fail := func(i int, msg string) {
		if len(errs) < maxErrorLines {
			errs = append(errs, DecodeError{Line: i + 1, Msg: msg})
		}
	}
	for i, f := range fs {
		_, p, _, ok := ParseLLMFingerprint(f.Fingerprint)
		if !ok {
			fail(i, "fingerprint is not rule:path[:symbol]")
			continue
		}
		listed := false
		for _, file := range f.Files {
			if file == p {
				listed = true
				break
			}
		}
		if !listed {
			fail(i, "fingerprint path is not in files")
			continue
		}
		if exists != nil && !exists(p) {
			fail(i, "fingerprint path does not exist")
		}
	}
	return errs
}

// LLMGrammarLines summarises errors as "invalid findings at lines 3, 7 (and
// N more)", for logs. It contains line numbers only.
func LLMGrammarLines(errs DecodeErrors) string {
	const show = 5
	var nums []string
	for i, e := range errs {
		if i == show {
			break
		}
		nums = append(nums, fmt.Sprint(e.Line))
	}
	s := "invalid findings at lines " + strings.Join(nums, ", ")
	if len(errs) > show {
		s += fmt.Sprintf(" (and %d more)", len(errs)-show)
	}
	return s
}
