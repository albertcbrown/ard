package query

import "testing"

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		q       RoleQuery
		want    string
		wantErr bool
	}{
		{
			name: "proposal example",
			q: RoleQuery{
				Request: "ard:triage:architect",
				States:  []string{"ard:triage:architect:running", "ard:triage:architect:done"},
				Guards:  []string{"ard:executor:failed", "ard:needs-human", "ard:hold"},
			},
			want: `is:open label:"ard:triage:architect" -label:"ard:triage:architect:running" -label:"ard:triage:architect:done" -label:ard:executor:failed -label:ard:needs-human -label:ard:hold`,
		},
		{
			name: "pr type",
			q:    RoleQuery{Type: TypePR, Request: "ard:review:security"},
			want: `is:open is:pr label:"ard:review:security"`,
		},
		{
			name: "any of",
			q:    RoleQuery{Type: TypeIssue, Request: "x", AnyOf: []string{"ard:p0", "ard:p1"}, Guards: []string{"ard:hold"}},
			want: `is:open is:issue label:"x" label:"ard:p0","ard:p1" -label:ard:hold`,
		},
		{
			name:    "any of with quote",
			q:       RoleQuery{Request: "x", AnyOf: []string{`a"b`}},
			wantErr: true,
		},
		{
			name: "issue type",
			q:    RoleQuery{Type: TypeIssue, Request: "x"},
			want: `is:open is:issue label:"x"`,
		},
		{
			name: "guard with space is quoted",
			q:    RoleQuery{Request: "r", Guards: []string{"needs work"}},
			want: `is:open label:"r" -label:"needs work"`,
		},
		{
			name: "lead form",
			q: RoleQuery{
				Request: "ard:pending",
				States:  []string{"ard:triage:lead:running", "ard:triage:lead:done"},
			},
			want: `is:open label:"ard:pending" -label:"ard:triage:lead:running" -label:"ard:triage:lead:done"`,
		},
		{name: "empty request", q: RoleQuery{}, wantErr: true},
		{name: "quote in request", q: RoleQuery{Request: `a"b`}, wantErr: true},
		{name: "quote in state", q: RoleQuery{Request: "a", States: []string{`b"`}}, wantErr: true},
		{name: "newline in guard", q: RoleQuery{Request: "a", Guards: []string{"b\nc"}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.q.Build()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}
