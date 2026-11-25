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
		{name: "numeric role", in: ok("role: 123\nstatus: done\nattempt: 1", "x"), wantErr: "field role: wrong type"},
		{name: "bool status", in: ok("role: a\nstatus: true\nattempt: 1", "x"), wantErr: "field status: wrong type"},
		{name: "float attempt", in: ok("role: a\nstatus: done\nattempt: 1.0", "x"), wantErr: "field attempt: wrong type"},
		{name: "numeric verdict", in: ok("role: a\nstatus: done\nattempt: 1\nverdict: 5", "x"), wantErr: "field verdict: wrong type"},
		{name: "numeric worker", in: ok("role: a\nstatus: done\nattempt: 1\nworker: 7", "x"), wantErr: "field worker: wrong type"},
		{name: "non-string next", in: ok("role: a\nstatus: done\nattempt: 1\nnext: [1, true]", "x"), wantErr: "field next: wrong type"},
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

func fetchedDoc(entry string) []byte {
	return []byte("---\nrole: engineer\nstatus: done\nattempt: 1\nfetched:\n" + entry + "---\nbody\n")
}

func TestParseFetched(t *testing.T) {
	sha := strings.Repeat("a1", 32)
	e := func(over map[string]string, drop string) string {
		f := map[string]string{
			"url": "https://example.com/a.json", "date": "2026-11-23", "status": "200", "sha256": sha,
			"verified_by": "https://example.com/sums.txt", "path": "testdata/a.json",
		}
		for k, v := range over {
			f[k] = v
		}
		delete(f, drop)
		s := ""
		first := true
		for _, k := range []string{"url", "date", "status", "sha256", "verified_by", "path"} {
			v, ok := f[k]
			if !ok {
				continue
			}
			p := "    "
			if first {
				p = "  - "
				first = false
			}
			s += p + k + ": " + v + "\n"
		}
		return s
	}
	o := map[string]string{}
	rf, err := Parse(fetchedDoc(e(o, "")))
	if err != nil || len(rf.Fetched) != 1 || rf.Fetched[0] != (Fetched{"https://example.com/a.json", "2026-11-23", 200, sha, "https://example.com/sums.txt", "testdata/a.json"}) {
		t.Fatalf("valid: %+v %v", rf.Fetched, err)
	}
	if _, err := Parse(fetchedDoc(e(map[string]string{"verified_by": "none"}, ""))); err != nil {
		t.Errorf("none: %v", err)
	}
	if rf, err := Parse([]byte("---\nrole: engineer\nstatus: done\nattempt: 1\n---\nx\n")); err != nil || rf.Fetched != nil {
		t.Errorf("absent: %v %v", rf.Fetched, err)
	}
	if _, err := Parse(fetchedDoc(strings.Repeat(e(o, ""), 50))); err != nil {
		t.Errorf("50 entries: %v", err)
	}
	bad := map[string]string{
		"51 entries":      strings.Repeat(e(o, ""), 51),
		"http":            e(map[string]string{"url": "http://example.com/a"}, ""),
		"ftp":             e(map[string]string{"url": "ftp://example.com/a"}, ""),
		"userinfo":        e(map[string]string{"url": "https://u:p@example.com/a"}, ""),
		"no host":         e(map[string]string{"url": "https:///a"}, ""),
		"backtick":        e(map[string]string{"url": `"https://example.com/a` + "`" + `b"`}, ""),
		"space":           e(map[string]string{"url": `"https://example.com/a b"`}, ""),
		"missing url":     e(o, "url"),
		"missing date":    e(o, "date"),
		"missing status":  e(o, "status"),
		"missing sha256":  e(o, "sha256"),
		"missing by":      e(o, "verified_by"),
		"missing path":    e(o, "path"),
		"sha 63":          e(map[string]string{"sha256": sha[:63]}, ""),
		"sha 65":          e(map[string]string{"sha256": sha + "a"}, ""),
		"sha non-hex":     e(map[string]string{"sha256": strings.Repeat("zz", 32)}, ""),
		"sha upper":       e(map[string]string{"sha256": strings.ToUpper(sha)}, ""),
		"sha all digits":  e(map[string]string{"sha256": strings.Repeat("12", 32)}, ""),
		"date month 13":   e(map[string]string{"date": "2026-13-40"}, ""),
		"date word":       e(map[string]string{"date": "yesterday"}, ""),
		"status 99":       e(map[string]string{"status": "99"}, ""),
		"status 600":      e(map[string]string{"status": "600"}, ""),
		"status string":   e(map[string]string{"status": `"200"`}, ""),
		"by http":         e(map[string]string{"verified_by": "http://example.com/s"}, ""),
		"by not url":      e(map[string]string{"verified_by": "someone"}, ""),
		"path absolute":   e(map[string]string{"path": "/etc/x"}, ""),
		"path dotdot":     e(map[string]string{"path": "a/../b"}, ""),
		"path backslash":  e(map[string]string{"path": `"a\\b"`}, ""),
		"path empty":      e(map[string]string{"path": `""`}, ""),
		"path backtick":   e(map[string]string{"path": "\"a`b\""}, ""),
		"path newline":    e(map[string]string{"path": `"a\nb"`}, ""),
		"path angle":      e(map[string]string{"path": `"a<b>"`}, ""),
		"unknown key":     e(o, "") + "    extra: x\n",
		"not a list":      "  url: https://example.com/a\n",
		"entry not a map": "  - just text\n",
	}
	for name, ent := range bad {
		_, err := Parse(fetchedDoc(ent))
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want ErrMalformed", name, err)
		}
	}
	if _, err := Parse(fetchedDoc(e(o, "") + e(map[string]string{"status": "99"}, ""))); err == nil || !strings.Contains(err.Error(), "fetched 2") {
		t.Errorf("message must name the entry: %v", err)
	}
}

func TestCheckFetchedStage(t *testing.T) {
	rf := RoleFile{Role: "r", Fetched: []Fetched{{}}}
	for stage, ok := range map[string]bool{StageWorker: true, StageReview: false, StageTriage: false} {
		err := rf.check(Job{Role: "r", Stage: stage})
		if stage == StageReview && err != nil && !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: %v", stage, err)
		}
		if ok && err != nil {
			t.Errorf("%s: %v", stage, err)
		}
		if !ok && !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: err = %v, want malformed", stage, err)
		}
	}
}

func TestParseNetwork(t *testing.T) {
	doc := func(status, list string) []byte {
		return []byte("---\nrole: engineer\nstatus: " + status + "\nattempt: 1\n" + list + "---\nx\n")
	}
	entry := func(what, url, why string) string {
		return "  - what: " + what + "\n    url: " + url + "\n    why: " + why + "\n"
	}
	ok := entry("spec", "https://example.com/a", "need")
	pad := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name   string
		status string
		list   string
		bad    string // "" = valid; else error substring
	}{
		{"one entry", "needs-network", "network:\n" + ok, ""},
		{"verify", "needs-network", "network:\n" + ok + "    verify: check the hash\n", ""},
		{"ten entries", "needs-network", "network:\n" + strings.Repeat(ok, 10), ""},
		{"eleven entries", "needs-network", "network:\n" + strings.Repeat(ok, 11), "network entries"},
		{"empty list", "needs-network", "network: []\n", "network entries"},
		{"missing list", "needs-network", "", "needs a network list"},
		{"with done", "done", "network:\n" + ok, "only with status"},
		{"with blocked", "blocked", "network:\n" + ok, "only with status"},
		{"with needs-human", "needs-human", "network:\n" + ok, "only with status"},
		{"http", "needs-network", "network:\n" + entry("a", "http://example.com/a", "b"), "https"},
		{"ftp", "needs-network", "network:\n" + entry("a", "ftp://example.com/a", "b"), "https"},
		{"userinfo", "needs-network", "network:\n" + entry("a", "https://u:p@example.com/x", "b"), "userinfo"},
		{"no host", "needs-network", "network:\n" + entry("a", "https:///x", "b"), "host"},
		{"backtick", "needs-network", "network:\n" + entry("a", `"https://example.com/a`+"`"+`b"`, "b"), "forbidden"},
		{"url 300", "needs-network", "network:\n" + entry("a", "https://example.com/"+pad(300-20), "b"), ""},
		{"url 301", "needs-network", "network:\n" + entry("a", "https://example.com/"+pad(301-20), "b"), "max 300"},
		{"what 200", "needs-network", "network:\n" + entry(pad(200), "https://example.com/a", "b"), ""},
		{"what 201", "needs-network", "network:\n" + entry(pad(201), "https://example.com/a", "b"), "what is over"},
		{"why 200 multibyte", "needs-network", "network:\n" + entry(strings.Repeat("é", 200), "https://example.com/a", "b"), ""},
		{"verify 201", "needs-network", "network:\n" + ok + "    verify: " + pad(201) + "\n", "verify is over"},
		{"multi-line", "needs-network", "network:\n  - what: |\n      a\n      b\n    url: https://example.com/a\n    why: c\n", "one line"},
		{"missing what", "needs-network", "network:\n  - url: https://example.com/a\n    why: c\n", "missing what"},
		{"empty why", "needs-network", "network:\n" + entry("a", "https://example.com/a", `""`), "empty why"},
		{"unknown key", "needs-network", "network:\n" + ok + "    extra: 1\n", "frontmatter"},
		{"non-string", "needs-network", "network:\n" + entry("5", "https://example.com/a", "b"), "wrong type"},
		{"second entry bad", "needs-network", "network:\n" + ok + entry("a", "http://x.com", "b"), "network 2"},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			rf, err := Parse(doc(c.status, c.list))
			if c.bad == "" {
				if err != nil || rf.Status != StatusNeedsNetwork || len(rf.Network) == 0 {
					t.Fatalf("got %+v %v", rf, err)
				}
				return
			}
			if !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), c.bad) {
				t.Fatalf("want malformed %q, got %v", c.bad, err)
			}
		})
	}
}

func TestCheckNeedsNetworkStage(t *testing.T) {
	rf := RoleFile{Role: "engineer", Status: StatusNeedsNetwork}
	for stage, wantErr := range map[string]bool{StageWorker: false, StageTriage: true, StageReview: true} {
		err := rf.check(Job{Stage: stage, Role: "engineer"})
		if (err != nil) != wantErr || (err != nil && !errors.Is(err, ErrMalformed)) {
			t.Errorf("%s: %v", stage, err)
		}
	}
}
