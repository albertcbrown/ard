package committer

import (
	"strings"
	"testing"
)

// Each case says whether GitHub would close the issue if the input text sat in
// a commit message or PR body (closes). The rule under test: NeutraliseRefs
// rewrites the text exactly when GitHub would close, except for the cases
// marked conservative, where GitHub would not close (or is unclear) but a
// rewrite is the safe direction.
func TestNeutraliseRefs(t *testing.T) {
	tests := []struct {
		name         string
		in, want     string
		closes       bool // GitHub would close the referenced issue
		conservative bool // rewritten although GitHub would not close
	}{
		{"close", "close #12", "close issue 12", true, false},
		{"closes", "closes #12", "closes issue 12", true, false},
		{"closed", "closed #12", "closed issue 12", true, false},
		{"fix", "fix #12", "fix issue 12", true, false},
		{"fixes", "fixes #12", "fixes issue 12", true, false},
		{"fixed", "fixed #12", "fixed issue 12", true, false},
		{"resolve", "resolve #12", "resolve issue 12", true, false},
		{"resolves", "resolves #12", "resolves issue 12", true, false},
		{"resolved", "resolved #12", "resolved issue 12", true, false},
		{"mixed case", "FiXeS #12", "FiXeS issue 12", true, false},
		{"colon", "Fixes: #12", "Fixes: issue 12", true, false},
		{"no space", "fixes#12", "fixes issue 12", true, false},
		{"cross repo", "Closes o/r#12", "Closes o/r issue 12", true, false},
		{"cross repo colon", "Closes: a.b/c-d#3", "Closes: a.b/c-d issue 3", true, false},
		{"GH-N", "Resolves GH-12", "Resolves issue 12", true, false},
		{"gh-N lower", "resolves gh-12", "resolves issue 12", true, false},
		{"issue url", "Fixes https://github.com/o/r/issues/12", "Fixes o/r issue 12", true, false},
		{"pull url http", "Fixes http://github.com/o/r/pull/12", "Fixes o/r issue 12", true, false},
		{"ghes url", "Closes https://ghe.example.com/o/r/issues/5", "Closes o/r issue 5", true, false},
		{"angle url", "Closes <https://github.com/o/r/issues/12>", "Closes <o/r issue 12>", true, false},
		{"markdown link", "Fixes [#12](https://github.com/o/r/issues/12)", "Fixes [issue 12](o/r issue 12)", true, false},
		{"markdown link text", "Fixes [link](https://github.com/o/r/issues/12)", "Fixes [link](o/r issue 12)", true, false},
		{"glued schemeless url", "fixes**github.com/o/r/issues/12", "fixes**o/r issue 12", true, false},
		{"schemeless markdown link", "Fixes [x](github.com/o/r/issues/12)", "Fixes [x](o/r issue 12)", true, false},
		{"nbsp", "Fixes\u00a0#12", "Fixes\u00a0issue 12", true, false},
		{"zwsp", "Fixes\u200b#12", "Fixes\u200bissue 12", true, false},
		{"word joiner", "Fixes\u2060#12", "Fixes\u2060issue 12", true, false},
		{"ideographic space", "Fixes\u3000#12", "Fixes\u3000issue 12", true, false},
		{"www url", "Fixes www.github.com/o/r/issues/12", "Fixes o/r issue 12", true, false},
		{"schemeless url", "Fixes github.com/o/r/issues/12", "Fixes o/r issue 12", true, false},
		{"fullwidth hash", "Fixes \uff03" + "12", "Fixes issue 12", true, false},
		{"fullwidth cross", "Fixes o/r\uff03" + "12", "Fixes o/r issue 12", true, false},
		{"emphasis", "Fixes **#12**", "Fixes **issue 12**", true, false},
		{"list", "Fixes #12, #13 and #14", "Fixes issue 12, issue 13 and issue 14", true, false},
		{"two keywords", "fixes #1; closes #2", "fixes issue 1; closes issue 2", true, false},
		{"newline split", "Fixes\n#12", "Fixes\nissue 12", true, false},
		{"keyword then apostrophe", "fixed #12's test", "fixed issue 12's test", true, false},
		{"no leading boundary", "suffix #12", "suffix issue 12", false, true},
		{"words between", "fixed a typo in #12's test", "fixed a typo in #12's test", false, false},
		{"no keyword", "see #12", "see #12", false, false},
		{"keyword no ref", "fixes the build", "fixes the build", false, false},
		{"empty", "", "", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NeutraliseRefs(tc.in)
			if got != tc.want {
				t.Fatalf("NeutraliseRefs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if rewritten := got != tc.in; rewritten != (tc.closes || tc.conservative) {
				t.Errorf("rewritten = %v, but GitHub closes = %v (conservative %v)", rewritten, tc.closes, tc.conservative)
			}
			if tc.closes && strings.Contains(got, "#") {
				t.Errorf("linkable reference remains in %q", got)
			}
			if again := NeutraliseRefs(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}
