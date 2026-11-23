package closeout

import "testing"

func TestMergeOwnSection(t *testing.T) {
	const own = "Implementation"
	tests := []struct {
		name, cur, mine, want string
		exists, err           bool
	}{
		{"other_section_changed_on_main", "## Requirements\nv2\n", "## Requirements\nv1\n## Implementation\nnew\n",
			"## Requirements\nv2\n## Implementation\nnew\n", true, false},
		{"own_replaced_in_place", "## Implementation\nold\n## Risk\nr\n", "## Implementation\nnew\n",
			"## Implementation\nnew\n## Risk\nr\n", true, false},
		{"own_removed_by_job", "## A\na\n## Implementation\nold\n", "## A\na\n", "## A\na\n", true, false},
		{"cur_missing", "", "## Implementation\nnew\n", "## Implementation\nnew\n", false, false},
		{"cur_unchanged", "## A\na\n## Implementation\nold\n", "## A\na\n## Implementation\nnew\n",
			"## A\na\n## Implementation\nnew\n", true, false},
		{"fenced_heading", "## A\n```\n## Implementation\n```\n", "## Implementation\nnew\n",
			"## A\n```\n## Implementation\n```\n## Implementation\nnew\n", true, false},
		{"missing_newline", "## A\na", "## Implementation\nnew\n", "## A\na\n## Implementation\nnew\n", true, false},
		{"duplicate_own_in_cur", "## Implementation\nx\n## Implementation\ny\n", "## Implementation\nnew\n", "", true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mergeOwnSection(own)([]byte(tc.cur), tc.exists, []byte(tc.mine))
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if err == nil && string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
