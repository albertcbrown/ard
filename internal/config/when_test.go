package config

import (
	"slices"
	"testing"
)

func TestWhenMatches(t *testing.T) {
	ran := func(roles ...string) func(string) bool {
		return func(r string) bool { return slices.Contains(roles, r) }
	}
	starter := &When{Paths: []string{"**/go.mod", "**/package*.json", "**/*.lock", "web/"}}
	tests := []struct {
		name    string
		w       *When
		changed []string
		ran     func(string) bool
		want    bool
	}{
		{"nil always", nil, nil, nil, true},
		{"dir deep", &When{Paths: []string{"web/"}}, []string{"web/a/b.tsx"}, nil, true},
		{"dir bare name", &When{Paths: []string{"web/"}}, []string{"web"}, nil, true},
		{"dir not prefix of segment", &When{Paths: []string{"web/"}}, []string{"webapp/x"}, nil, false},
		{"dir anchored at root", &When{Paths: []string{"web/"}}, []string{"a/web/x"}, nil, false},
		{"** root file", &When{Paths: []string{"**/go.mod"}}, []string{"go.mod"}, nil, true},
		{"** nested file", &When{Paths: []string{"**/go.mod"}}, []string{"a/b/go.mod"}, nil, true},
		{"case sensitive", &When{Paths: []string{"**/go.mod"}}, []string{"Go.mod"}, nil, false},
		{"slash-free glob at root", &When{Paths: []string{"package*.json"}}, []string{"package.json"}, nil, true},
		{"slash-free glob not nested", &When{Paths: []string{"package*.json"}}, []string{"sub/package.json"}, nil, false},
		{"exact", &When{Paths: []string{"go.mod"}}, []string{"go.mod"}, nil, true},
		{"exact not nested", &When{Paths: []string{"go.mod"}}, []string{"x/go.mod"}, nil, false},
		{"lock", &When{Paths: []string{"**/*.lock"}}, []string{"a/yarn.lock"}, nil, true},
		{"triage ran", &When{Triage: []string{"architect"}}, []string{}, ran("architect"), true},
		{"triage not ran", &When{Triage: []string{"architect"}}, []string{}, ran("lead"), false},
		{"triage nil func", &When{Triage: []string{"architect"}}, []string{}, nil, false},
		{"paths or triage", &When{Paths: []string{"go.mod"}, Triage: []string{"architect"}}, []string{"x"}, ran("architect"), true},
		{"paths miss triage miss", &When{Paths: []string{"go.mod"}, Triage: []string{"architect"}}, []string{"x"}, nil, false},
		{"empty changed", &When{Paths: []string{"go.mod"}}, []string{}, nil, false},
		{"unknown changed fails open", &When{Paths: []string{"go.mod"}}, nil, nil, true},
		{"unknown changed, triage-only rule", &When{Triage: []string{"architect"}}, nil, nil, false},
		{"starter list hit", starter, []string{"cmd/x/main.go", "web/app.ts"}, nil, true},
		{"starter list miss", starter, []string{"cmd/x/main.go", "README.md"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.Matches(tt.changed, tt.ran); got != tt.want {
				t.Errorf("Matches = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectReviewers(t *testing.T) {
	rev := func(name string, w *When) Role { return Role{Kind: KindReviewer, Name: name, When: w} }
	r := &Resolved{Roles: []Role{
		rev("a-eng", nil),
		rev("b-license", &When{Paths: []string{"**/go.mod"}}),
		rev("c-arch", &When{Triage: []string{"architect"}}),
	}}
	none := func(string) bool { return false }
	arch := func(s string) bool { return s == "architect" }
	tests := []struct {
		name    string
		worker  Role
		changed []string
		ran     func(string) bool
		want    []string
	}{
		{"ungated only", Role{}, []string{"x.go"}, none, []string{"a-eng"}},
		{"path hit", Role{}, []string{"go.mod"}, none, []string{"a-eng", "b-license"}},
		{"triage hit", Role{}, []string{"x.go"}, arch, []string{"a-eng", "c-arch"}},
		{"all, config order", Role{}, []string{"go.mod"}, arch, []string{"a-eng", "b-license", "c-arch"}},
		{"worker list narrows", Role{Reviewers: []string{"b-license", "a-eng"}}, []string{"go.mod"}, arch, []string{"a-eng", "b-license"}},
		{"worker list honours when", Role{Reviewers: []string{"b-license"}}, []string{"x.go"}, none, nil},
		{"unknown names ignored", Role{Reviewers: []string{"ghost", "a-eng"}}, nil, none, []string{"a-eng"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.SelectReviewers(tt.worker, tt.changed, tt.ran)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
