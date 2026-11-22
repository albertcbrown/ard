package closeout

import (
	"reflect"
	"strings"
	"testing"
)

func TestWritableFor(t *testing.T) {
	tests := []struct {
		name              string
		stage, role, sect string
		wantSection       string
		wantPrefixes      []string
		wantRoleFile      string
	}{
		{"lead: role file only", StageTriage, "lead", "", "", nil, ".ard/7/triage-lead.md"},
		{"product-manager", StageTriage, "product-manager", "Requirements", "Requirements", nil, ".ard/7/triage-product-manager.md"},
		{"designer: section and design/", StageTriage, "designer", "UX", "UX", []string{".ard/7/design/"}, ".ard/7/triage-designer.md"},
		{"custom role with a section", StageTriage, "security-champion", "Security", "Security", nil, ".ard/7/triage-security-champion.md"},
		{"custom role without one", StageTriage, "security-champion", "", "", nil, ".ard/7/triage-security-champion.md"},
		{"worker never gets a section", StageWorker, "engineer", "Implementation", "", nil, ".ard/7/worker-engineer.md"},
		{"reviewer", StageReview, "eng-review", "", "", nil, ".ard/7/review-eng-review.md"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := WritableFor(7, tc.stage, tc.role, tc.sect)
			if w.RoleFile != tc.wantRoleFile || w.Section != tc.wantSection || !reflect.DeepEqual(w.Prefixes, tc.wantPrefixes) {
				t.Errorf("WritableFor = %+v", w)
			}
		})
	}
}

func TestCheckPlan(t *testing.T) {
	const (
		base    = "# Plan for #7\n\n## Requirements\n\nreq\n\n## Design\n\ndesign\n"
		ownImpl = "## Implementation\n\nsteps\n"
	)
	tests := []struct {
		name   string
		base   string // "" with noBase false means absent
		noBase bool
		cur    string
		own    string
		want   string // "" = allowed, else substring of the reason
	}{
		{name: "new file, own section only", noBase: true, cur: ownImpl, own: "Implementation"},
		{name: "new file with a title", noBase: true, cur: "# Plan for #7\n\n" + ownImpl, own: "Implementation"},
		{name: "new file with another section", noBase: true, cur: "## Design\nx\n", own: "Implementation", want: "adds section ## Design"},
		{name: "append own section", base: base, cur: base + "\n" + ownImpl, own: "Implementation"},
		{name: "replace own section in place", base: base + "\n## Implementation\n\nold\n", cur: base + "\n" + ownImpl, own: "Implementation"},
		{name: "own section moved", base: base + "\n" + ownImpl, cur: "# Plan for #7\n\n" + ownImpl + "\n## Requirements\n\nreq\n\n## Design\n\ndesign\n", own: "Implementation"},
		{name: "own section removed", base: base + "\n" + ownImpl, cur: base, own: "Implementation"},
		{name: "other section edited", base: base, cur: strings.Replace(base, "design\n", "mine\n", 1) + ownImpl, own: "Implementation", want: "changes section ## Design"},
		{name: "other section deleted", base: base, cur: "# Plan for #7\n\n## Requirements\n\nreq\n\n" + ownImpl, own: "Implementation", want: "removes section ## Design"},
		{name: "other section in the middle deleted", base: base + "\n## Docs\n\nd\n", cur: strings.Replace(base+"\n## Docs\n\nd\n", "## Design\n\ndesign\n\n", "", 1), own: "Implementation", want: "removes section ## Design"},
		{name: "sections swapped", base: base, cur: "# Plan for #7\n\n## Design\n\ndesign\n\n## Requirements\n\nreq\n", own: "Implementation", want: "changes section"},
		{name: "preamble edited", base: base, cur: strings.Replace(base, "# Plan for #7", "# Mine", 1), own: "Implementation", want: "changed text before the first section"},
		{name: "own heading twice", base: base, cur: base + ownImpl + ownImpl, own: "Implementation", want: "appears twice"},
		{name: "fenced ## inside own section", base: base, cur: base + "\n## Implementation\n\n```\n## Design\nx\n```\n", own: "Implementation"},
		{name: "tilde fence", base: base, cur: base + "\n## Implementation\n\n~~~\n## Design\n~~~\n", own: "Implementation"},
		{name: "unclosed fence swallows Design", base: base, cur: "# Plan for #7\n\n## Requirements\n\nreq\n\n## Implementation\n\n```\n\n## Design\n\ndesign\n", own: "Implementation", want: "removes section ## Design"},
		{name: "closing hashes", base: "## Design ##\n\nd\n", cur: "## Design ##\n\nd\n\n" + ownImpl, own: "Implementation"},
		{name: "wrong case is a new section", base: base, cur: base + "\n## implementation\n\nx\n", own: "Implementation", want: "adds section ## implementation"},
		{name: "crlf base unchanged", base: strings.ReplaceAll(base, "\n", "\r\n"), cur: strings.ReplaceAll(base, "\n", "\r\n") + "\r\n" + ownImpl, own: "Implementation"},
		{name: "indented heading", base: base, cur: base + "\n## Implementation\n\nx\n ## Design\nevil\n", own: "Implementation", want: "section ## Design"},
		{name: "tab after ##", base: base, cur: base + "\n## Implementation\n\nx\n##\tDesign\nevil\n", own: "Implementation", want: "section ## Design"},
		{name: "setext heading", base: base, cur: base + "\n## Implementation\n\nDesign\n------\nevil\n", own: "Implementation", want: "heading-like line in own section"},
		{name: "setext in a new file's preamble", noBase: true, cur: "Title\n=====\n\n" + ownImpl, own: "Implementation", want: "heading-like line in own section"},
		{name: "lone CR", base: base, cur: base + "\n## Implementation\n\nx\r## Design\nevil\n", own: "Implementation", want: "section ## Design"},
		{name: "invalid backtick fence", base: base, cur: base + "\n## Implementation\n\n```a`b\n## Design\nevil\n```\n", own: "Implementation", want: "section ## Design"},
		{name: "heading hidden in a longer fence is not a close", base: base, cur: "# Plan for #7\n\n## Requirements\n\nreq\n\n## Implementation\n\n````\n```\n## Design\n````\n", own: "Implementation", want: "removes section ## Design"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPlan([]byte(tc.base), !tc.noBase, []byte(tc.cur), tc.own)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Errorf("checkPlan = %q, want %q", got, tc.want)
			}
		})
	}
}
