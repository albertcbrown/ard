package github

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		in      string
		want    Repo
		wantErr bool
	}{
		{"o/r", Repo{"o", "r"}, false},
		{"o", Repo{}, true},
		{"a/b/c", Repo{}, true},
		{"/r", Repo{}, true},
		{"", Repo{}, true},
	}
	for _, tc := range tests {
		got, err := ParseRepo(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseRepo(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestRepoFromRemote(t *testing.T) {
	want := Repo{"o", "r"}
	tests := []struct {
		in      string
		want    Repo
		wantErr bool
	}{
		{"https://github.com/o/r", want, false},
		{"https://github.com/o/r.git", want, false},
		{"git@github.com:o/r.git", want, false},
		{"git@github.com:o/r", want, false},
		{"ssh://git@github.com/o/r", want, false},
		{"ssh://git@github.com/o/r.git", want, false},
		{"https://gitlab.com/o/r", Repo{}, true},
		{"git@gitlab.com:o/r.git", Repo{}, true},
		{"https://github.com/a/b/c", Repo{}, true},
		{"/local/path", Repo{}, true},
	}
	for _, tc := range tests {
		got, err := RepoFromRemote(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("RepoFromRemote(%q) = %v, %v", tc.in, got, err)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate limit", &RateLimitError{}, true},
		{"wrapped rate limit", fmt.Errorf("x: %w", &RateLimitError{Secondary: true}), true},
		{"500", &HTTPError{Status: 500}, true},
		{"503", &HTTPError{Status: 503}, true},
		{"422", &HTTPError{Status: 422}, false},
		{"timeout", timeoutErr{}, true},
		{"canceled", context.Canceled, false},
		{"not found", ErrNotFound, false},
		{"other", errors.New("boom"), false},
	}
	for _, tc := range tests {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("%s: IsTransient = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPermissionAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		p    Permission
		want bool
	}{
		{Permission{"write", "write"}, true},
		{Permission{"read", "triage"}, true},
		{Permission{"read", "read"}, false},
		{Permission{"none", ""}, false},
		{Permission{"admin", ""}, true},
	} {
		if got := tc.p.AtLeastTriage(); got != tc.want {
			t.Errorf("%+v AtLeastTriage = %v", tc.p, got)
		}
	}
	id := Identity{Login: "ard[bot]", ID: 42}
	if got := id.NoreplyEmail(); got != "42+ard[bot]@users.noreply.github.com" {
		t.Errorf("NoreplyEmail = %q", got)
	}
}
