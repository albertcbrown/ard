package executor

import (
	"errors"
	"testing"
	"time"
)

func goodJob() Job {
	return Job{ID: "01ABC", Repo: "o/r", Stage: "worker", Role: "go-dev", RepoDir: "/r", Ref: "origin/main", RoleDir: "/role", Timeout: time.Minute}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Job)
		ok   bool
	}{
		{"good", func(*Job) {}, true},
		{"empty id", func(j *Job) { j.ID = "" }, false},
		{"slash id", func(j *Job) { j.ID = "a/b" }, false},
		{"dot repo owner", func(j *Job) { j.Repo = "./r" }, false},
		{"dot repo name", func(j *Job) { j.Repo = "o/." }, false},
		{"backslash repo", func(j *Job) { j.Repo = `o\x/r` }, false},
		{"space repo", func(j *Job) { j.Repo = "o/r r" }, false},
		{"upper repo", func(j *Job) { j.Repo = "O/R" }, true},
		{"bad stage", func(j *Job) { j.Stage = "x" }, false},
		{"bad role", func(j *Job) { j.Role = "Go_Dev" }, false},
		{"zero timeout", func(j *Job) { j.Timeout = 0 }, false},
		{"relative repodir", func(j *Job) { j.RepoDir = "r" }, false},
		{"relative roledir", func(j *Job) { j.RoleDir = "role" }, false},
		{"abs input", func(j *Job) { j.Inputs = []File{{Path: "/etc/x"}} }, false},
		{"dotdot input", func(j *Job) { j.Inputs = []File{{Path: "a/../../x"}} }, false},
		{"git input", func(j *Job) { j.Inputs = []File{{Path: ".git/config"}} }, false},
		{"ok input", func(j *Job) { j.Inputs = []File{{Path: ".ard/1/issue.md"}} }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := goodJob()
			tc.mut(&j)
			err := j.Validate()
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidJob) {
				t.Fatalf("want ErrInvalidJob, got %v", err)
			}
		})
	}
}

func TestTerminal(t *testing.T) {
	for s, want := range map[State]bool{Prepared: false, Running: false, Orphaned: false, Exited: true, TimedOut: true, Gone: true} {
		if s.Terminal() != want {
			t.Errorf("%s", s)
		}
	}
}
