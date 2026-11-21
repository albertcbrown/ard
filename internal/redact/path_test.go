package redact

import (
	"errors"
	"testing"
)

func TestCommitPath(t *testing.T) {
	tests := []struct {
		issue int
		log   string
		want  string
		bad   bool
	}{
		{12, "/x/worker-engineer-20261121T101500Z.log", ".ard/12/logs/worker-engineer-20261121T101500Z.log", false},
		{3, "/h/review-security-review-20261121T101500Z.log", ".ard/3/logs/review-security-review-20261121T101500Z.log", false},
		{12, "/x/worker-engineer-20261121T101500.123Z.log", ".ard/12/logs/worker-engineer-20261121T101500.123Z.log", false},
		{1, "/x/worker-engineer-20261121T101500.12Z.log", "", true},
		{0, "/x/a.log", "", true},
		{-1, "/x/a.log", "", true},
		{1, "", "", true},
		{1, "..", "", true},
		{1, "/x/..", "", true},
		{1, "/x/Worker-1.log", "", true},
		{1, "/x/a.txt", "", true},
		{1, "/x/a..b.log", "", true},
		{1, "/x/-a.log", "", true},
	}
	for _, tc := range tests {
		got, err := CommitPath(tc.issue, tc.log)
		if tc.bad {
			if !errors.Is(err, ErrBadPath) {
				t.Errorf("CommitPath(%d,%q) = %q, %v", tc.issue, tc.log, got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("CommitPath(%d,%q) = %q, %v", tc.issue, tc.log, got, err)
		}
	}
}
