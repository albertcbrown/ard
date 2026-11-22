package gitref

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	sha := strings.Repeat("ab12", 10)
	valid := []string{"main", "v1.2.0", "release/2026-11", "refs/heads/main", sha, "abc", "feature_x", "a@b"}
	for _, r := range valid {
		if err := Check(r); err != nil {
			t.Errorf("Check(%q) = %v; want nil", r, err)
		}
	}
	invalid := []struct{ ref, want string }{
		{"", "empty"},
		{strings.Repeat("a", 256) + "x", "longer"},
		{"-x", `"-"`},
		{"/x", "start or end"},
		{"x/", "start or end"},
		{"x.", `end with "."`},
		{"x.lock", ".lock"},
		{"a..b", `".."`},
		{"a//b", `"//"`},
		{"a@{1}", `"@{"`},
		{"@", `"@"`},
		{"a:b", `":"`},
		{"a b", `" "`},
		{"a?b", `"?"`},
		{"a*b", `"*"`},
		{"a[b", `"["`},
		{`a\b`, "must not contain"},
		{"a^b", `"^"`},
		{"a~b", `"~"`},
		{"a\x01b", "must not contain"},
		{"a\x7fb", "must not contain"},
		{".hidden", "start with"},
		{"a/.b", "start with"},
		{"abc1234", "abbreviated SHA"},
		{strings.Repeat("a", 39), "abbreviated SHA"},
		{strings.ToUpper(sha), "lowercase"},
	}
	for _, tc := range invalid {
		err := Check(tc.ref)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Check(%q) = %v; want error containing %q", tc.ref, err, tc.want)
		}
	}
}

func TestIsSHA(t *testing.T) {
	sha := strings.Repeat("0f", 20)
	for ref, want := range map[string]bool{sha: true, strings.ToUpper(sha): false, sha[:39]: false, sha + "a": false, "main": false, "": false} {
		if got := IsSHA(ref); got != want {
			t.Errorf("IsSHA(%q) = %v; want %v", ref, got, want)
		}
	}
}
