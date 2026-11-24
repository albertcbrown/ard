package roles

import (
	"regexp"
	"strings"
	"testing"
)

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// onlyHeadings reports the first line that is neither blank nor a heading once
// HTML comments are removed.
func onlyHeadings(s string) (string, bool) {
	for _, l := range strings.Split(htmlComment.ReplaceAllString(s, ""), "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return l, false
		}
	}
	return "", true
}

func TestContextTemplates(t *testing.T) {
	for _, name := range []string{"PRODUCT.md", "VISION.md", "TECH_STACK.md"} {
		b, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if l, ok := onlyHeadings(s); !ok {
			t.Errorf("%s has a prose line outside comments: %q", name, l)
		}
		if !strings.Contains(s, "\nLast reviewed: YYYY-MM-DD\n") {
			t.Errorf("%s lacks the Last reviewed line", name)
		}
		if !strings.Contains(s, "150 lines") {
			t.Errorf("%s lacks the size guidance", name)
		}
		if !strings.Contains(s, "\n# ") {
			t.Errorf("%s has no headings", name)
		}
	}
	if _, ok := onlyHeadings("# A\n\nsome prose\n"); ok {
		t.Error("prose line not detected")
	}
}
