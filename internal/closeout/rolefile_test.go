package closeout

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	ok := func(front, body string) string { return "---\n" + front + "\n---\n" + body }
	tests := []struct {
		name    string
		in      string
		want    RoleFile
		wantErr string // substring of the ErrMalformed reason; "" = valid
	}{
		{name: "done", in: ok("role: engineer\nstatus: done\nattempt: 1", "All done.\n"),
			want: RoleFile{Role: "engineer", Status: "done", Attempt: 1, Summary: "All done."}},
		{name: "blocked", in: ok("role: engineer\nstatus: blocked\nattempt: 2", "Stuck."),
			want: RoleFile{Role: "engineer", Status: "blocked", Attempt: 2, Summary: "Stuck."}},
		{name: "needs-human", in: ok("role: a\nstatus: needs-human\nattempt: 1", "Help."),
			want: RoleFile{Role: "a", Status: "needs-human", Attempt: 1, Summary: "Help."}},
		{name: "proposal example", in: ok("role: engineer\nstatus: done          # done | blocked | needs-human\nattempt: 1\nnext: [review:security-review, review:eng-review]   # optional requests\nworker: engineer      # lead only", "Short summary."),
			want: RoleFile{Role: "engineer", Status: "done", Attempt: 1, Worker: "engineer", Next: []string{"review:security-review", "review:eng-review"}, Summary: "Short summary."}},
		{name: "verdict", in: ok("role: r\nstatus: done\nattempt: 1\nverdict: changes", "Fix it."),
			want: RoleFile{Role: "r", Status: "done", Attempt: 1, Verdict: "changes", Summary: "Fix it."}},
		{name: "BOM and CRLF", in: "\xef\xbb\xbf---\r\nrole: a\r\nstatus: done\r\nattempt: 1\r\n---\r\nBody\r\n",
			want: RoleFile{Role: "a", Status: "done", Attempt: 1, Summary: "Body"}},
		{name: "summary may hold a fence", in: ok("role: a\nstatus: done\nattempt: 1", "one\n---\ntwo"),
			want: RoleFile{Role: "a", Status: "done", Attempt: 1, Summary: "one\n---\ntwo"}},
		{name: "no opening fence", in: "role: a\nstatus: done\n", wantErr: "no opening"},
		{name: "no closing fence", in: "---\nrole: a\nstatus: done\nattempt: 1\n", wantErr: "no closing"},
		{name: "empty doc", in: "---\n---\nbody", wantErr: "empty frontmatter"},
		{name: "bad yaml", in: ok("role: [a\nstatus: done", "x"), wantErr: "frontmatter"},
		{name: "unknown key", in: ok("role: a\nstatus: done\nattempt: 1\nbogus: 1", "x"), wantErr: "bogus"},
		{name: "duplicate key", in: ok("role: a\nrole: b\nstatus: done\nattempt: 1", "x"), wantErr: "already defined"},
		{name: "string attempt", in: ok("role: a\nstatus: done\nattempt: \"1\"", "x"), wantErr: "cannot unmarshal"},
		{name: "attempt zero", in: ok("role: a\nstatus: done\nattempt: 0", "x"), wantErr: "below 1"},
		{name: "missing role", in: ok("status: done\nattempt: 1", "x"), wantErr: "missing role"},
		{name: "missing status", in: ok("role: a\nattempt: 1", "x"), wantErr: "missing status"},
		{name: "missing attempt", in: ok("role: a\nstatus: done", "x"), wantErr: "missing attempt"},
		{name: "bad status", in: ok("role: a\nstatus: finished\nattempt: 1", "x"), wantErr: "not done, blocked"},
		{name: "bad verdict", in: ok("role: a\nstatus: done\nattempt: 1\nverdict: lgtm", "x"), wantErr: "not approve or changes"},
		{name: "next scalar", in: ok("role: a\nstatus: done\nattempt: 1\nnext: review:x", "x"), wantErr: "cannot unmarshal"},
		{name: "empty summary", in: ok("role: a\nstatus: done\nattempt: 1", "  \n"), wantErr: "empty summary"},
		{name: "too big", in: ok("role: a\nstatus: done\nattempt: 1", strings.Repeat("x", MaxRoleFileBytes)), wantErr: "max"},
		{name: "NUL", in: ok("role: a\nstatus: done\nattempt: 1", "a\x00b"), wantErr: "UTF-8"},
		{name: "bad UTF-8", in: ok("role: a\nstatus: done\nattempt: 1", "a\xffb"), wantErr: "UTF-8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.in))
			if tc.wantErr != "" {
				if !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want ErrMalformed containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Role != tc.want.Role || got.Status != tc.want.Status || got.Attempt != tc.want.Attempt ||
				got.Verdict != tc.want.Verdict || got.Worker != tc.want.Worker || got.Summary != tc.want.Summary ||
				strings.Join(got.Next, ",") != strings.Join(tc.want.Next, ",") {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	base := RoleFile{Role: "r", Status: "done", Attempt: 1, Summary: "s"}
	with := func(f func(*RoleFile)) RoleFile { rf := base; f(&rf); return rf }
	tests := []struct {
		name string
		j    Job
		rf   RoleFile
		bad  bool
	}{
		{"ok worker", Job{Stage: StageWorker, Role: "r"}, base, false},
		{"role mismatch", Job{Stage: StageWorker, Role: "x"}, base, true},
		{"reviewer done needs verdict", Job{Stage: StageReview, Role: "r"}, base, true},
		{"reviewer done approve", Job{Stage: StageReview, Role: "r"}, with(func(r *RoleFile) { r.Verdict = "approve" }), false},
		{"reviewer blocked no verdict", Job{Stage: StageReview, Role: "r"}, with(func(r *RoleFile) { r.Status = "blocked" }), false},
		{"verdict on worker", Job{Stage: StageWorker, Role: "r"}, with(func(r *RoleFile) { r.Verdict = "approve" }), true},
		{"worker on non-lead", Job{Stage: StageTriage, Role: "r"}, with(func(r *RoleFile) { r.Worker = "w" }), true},
		{"worker on lead", Job{Stage: StageTriage, Role: "lead"}, with(func(r *RoleFile) { r.Role = "lead"; r.Worker = "w" }), false},
	}
	for _, tc := range tests {
		err := tc.rf.check(tc.j)
		if (err != nil) != tc.bad || (err != nil && !errors.Is(err, ErrMalformed)) {
			t.Errorf("%s: err = %v, bad = %v", tc.name, err, tc.bad)
		}
	}
}

func TestReadRoleFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := readRoleFile(filepath.Join(dir, "nope.md")); !errors.Is(err, ErrMissing) {
		t.Errorf("missing: %v", err)
	}
	target := filepath.Join(dir, "real.md")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readRoleFile(link); !errors.Is(err, ErrMalformed) {
		t.Errorf("symlink: %v", err)
	}
	if _, err := readRoleFile(dir); !errors.Is(err, ErrMalformed) {
		t.Errorf("directory: %v", err)
	}
	if b, err := readRoleFile(target); err != nil || string(b) != "x" {
		t.Errorf("regular: %q %v", b, err)
	}
}
