package executor

import (
	"errors"
	"strings"
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
		{"scanner trusted input", func(j *Job) { j.Stage = "scanner"; j.TrustedInput = true }, true},
		{"scanner history", func(j *Job) { j.Stage = "scanner"; j.History = true }, true},
		{"triage history", func(j *Job) { j.Stage = "triage"; j.History = true }, false},
		{"worker history", func(j *Job) { j.Stage = "worker"; j.History = true }, false},
		{"review history", func(j *Job) { j.Stage = "review"; j.History = true }, false},
		{"triage trusted input", func(j *Job) { j.Stage = "triage"; j.TrustedInput = true }, false},
		{"scanner output", func(j *Job) { j.Stage = "scanner"; j.ReadOnly = true; j.Output = true }, true},
		{"triage output", func(j *Job) { j.Stage = "triage"; j.Output = true }, false},
		{"worker output", func(j *Job) { j.Stage = "worker"; j.Output = true }, false},
		{"review output", func(j *Job) { j.Stage = "review"; j.Output = true }, false},
		{"network advisory", func(j *Job) { j.Network = NetworkAdvisory }, true},
		{"network none", func(j *Job) { j.Network = NetworkNone }, true},
		{"network bogus", func(j *Job) { j.Network = "bogus" }, false},
		{"zero timeout", func(j *Job) { j.Timeout = 0 }, false},
		{"relative repodir", func(j *Job) { j.RepoDir = "r" }, false},
		{"relative roledir", func(j *Job) { j.RoleDir = "role" }, false},
		{"abs input", func(j *Job) { j.Inputs = []File{{Path: "/etc/x"}} }, false},
		{"dotdot input", func(j *Job) { j.Inputs = []File{{Path: "a/../../x"}} }, false},
		{"git input", func(j *Job) { j.Inputs = []File{{Path: ".git/config"}} }, false},
		{"valid sidecar", func(j *Job) { j.Sidecar = Sidecar{Repo: "acme/specs", Ref: "main"} }, true},
		{"sidecar sha ref", func(j *Job) { j.Sidecar = Sidecar{Repo: "acme/specs", Ref: strings.Repeat("ab", 20)} }, true},
		{"bad sidecar repo", func(j *Job) { j.Sidecar = Sidecar{Repo: "specs", Ref: "main"} }, false},
		{"sidecar ref colon", func(j *Job) { j.Sidecar = Sidecar{Repo: "a/b", Ref: "a:b"} }, false},
		{"sidecar ref dash", func(j *Job) { j.Sidecar = Sidecar{Repo: "a/b", Ref: "-x"} }, false},
		{"sidecar ref empty", func(j *Job) { j.Sidecar = Sidecar{Repo: "a/b"} }, false},
		{"sidecar ref without repo", func(j *Job) { j.Sidecar = Sidecar{Ref: "main"} }, false},
		{"zero sidecar", func(j *Job) { j.Sidecar = Sidecar{} }, true},
		{"scrub role file", func(j *Job) { j.Issue = 7; j.Scrub = []string{".ard/7/worker-dev.md"} }, true},
		{"scrub without issue", func(j *Job) { j.Scrub = []string{".ard/7/worker-dev.md"} }, false},
		{"scrub other issue", func(j *Job) { j.Issue = 7; j.Scrub = []string{".ard/8/worker-dev.md"} }, false},
		{"scrub outside .ard", func(j *Job) { j.Issue = 7; j.Scrub = []string{"README.md"} }, false},
		{"scrub nested", func(j *Job) { j.Issue = 7; j.Scrub = []string{".ard/7/logs/x.log"} }, false},
		{"scrub dotdot", func(j *Job) { j.Issue = 7; j.Scrub = []string{".ard/7/../8/x.md"} }, false},
		{"scrub also an input", func(j *Job) {
			j.Issue = 7
			j.Scrub = []string{".ard/7/worker-dev.md"}
			j.Inputs = []File{{Path: ".ard/7/worker-dev.md"}}
		}, false},
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

func TestCheckImageRef(t *testing.T) {
	d := strings.Repeat("a1", 32)
	for _, ok := range []string{"x/y@sha256:" + d, "sha256:0123456789ab", "sha256:" + d} {
		if err := CheckImageRef(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "ard-agent:dev", "-x", "a b", "x@sha256:abc", "x@sha256:" + strings.Repeat("g", 64), "sha256:abc", "-v/x@sha256:" + d, "x\n@sha256:" + d} {
		if err := CheckImageRef(bad); !errors.Is(err, ErrImageRef) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
