package redact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustPaths(t *testing.T, s *Secrets, rules ...PathRule) *Secrets {
	t.Helper()
	n, err := s.WithPaths(rules...)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWithPathsValidation(t *testing.T) {
	const secretFrom = "/Users/zed-the-operator/x"
	good := PathRule{Name: "path.home", From: secretFrom, To: "~"}
	tests := map[string]PathRule{
		"relative":  {Name: "n", From: "Users/zed-the-operator", To: "~"},
		"empty":     {Name: "n", From: "", To: "~"},
		"root":      {Name: "n", From: "/", To: "~"},
		"slashes":   {Name: "n", From: "//", To: "~"},
		"nul":       {Name: "n", From: "/Users/zed\x00", To: "~"},
		"newline":   {Name: "n", From: "/Users/zed\n", To: "~"},
		"empty to":  {Name: "n", From: secretFrom, To: ""},
		"bad name":  {Name: "bad name", From: secretFrom, To: "~"},
		"empty nam": {Name: "", From: secretFrom, To: "~"},
	}
	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := NewSecrets().WithPaths(good, r)
			if !errors.Is(err, ErrBadPathRule) {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), "zed") {
				t.Errorf("error leaks the path: %v", err)
			}
		})
	}
	s := NewSecrets()
	_ = s.Add("K", "a-long-secret-value")
	n := mustPaths(t, s, PathRule{Name: "p", From: "/Users/al///", To: "~"}, PathRule{Name: "q", From: "/root", To: "~r"})
	if got := n.String("/Users/al/x /root/y"); got != "~/x ~r/y" {
		t.Errorf("got %q", got)
	}
	if got := s.String("/Users/al/x"); got != "/Users/al/x" {
		t.Errorf("s changed: %q", got)
	}
	if _, err := (*Secrets)(nil).WithPaths(good); err != nil {
		t.Errorf("nil receiver: %v", err)
	}
	dup := mustPaths(t, nil, PathRule{Name: "a", From: "/Users/al", To: "A"}, PathRule{Name: "b", From: "/Users/al/", To: "B"})
	if got := dup.String("/Users/al/x"); got != "A/x" {
		t.Errorf("first rule must win: %q", got)
	}
}

func TestPathBoundary(t *testing.T) {
	s := mustPaths(t, nil, PathRule{Name: "path.home", From: "/Users/al", To: "~"})
	for in, want := range map[string]string{
		"/Users/al/x":         "~/x",
		"/Users/al":           "~",
		`"/Users/al"`:         `"~"`,
		"/Users/al.":          "~.",
		"/Users/al:":          "~:",
		"/Users/al\n":         "~\n",
		"/Users/alice/x":      "/Users/alice/x",
		"/Users/al_x":         "/Users/al_x",
		"/Users/al-x":         "/Users/al-x",
		"/Users/alé":          "~é",
		"/Users/al—see":       "~—see",
		"/Users/al’s":         "~’s",
		"/Users/al\u00a0x":    "~\u00a0x",
		"/Users/al。":          "~。",
		"x/Users/al/y":        "x~/y",
		"/Users/al /Users/al": "~ ~",
	} {
		if got := s.String(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestPathLongestFirst(t *testing.T) {
	const h = "/Users/al"
	const r = h + "/.ard"
	const j = r + "/work/o/r/J"
	s := mustPaths(t, nil,
		PathRule{Name: "path.home", From: h, To: "~"},
		PathRule{Name: "path.ard-root", From: r, To: "~/.ard"},
		PathRule{Name: "path.job-home", From: j + ".home", To: "/ard-home"},
		PathRule{Name: "path.work", From: j, To: "/work"},
	)
	for in, want := range map[string]string{
		j + "/a":                "/work/a",
		j + ".home/.claude/x":   "/ard-home/.claude/x",
		r + "/work/o/r/JX":      "~/.ard/work/o/r/JX",
		r + "/logs/o/r/5/x.log": "~/.ard/logs/o/r/5/x.log",
		h + "/src":              "~/src",
	} {
		if got := s.String(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestPathEscapedForms(t *testing.T) {
	s := mustPaths(t, nil, PathRule{Name: "path.home", From: "/Users/al", To: "~"})
	if got := s.String(`{"cmd":"cd \/Users\/al\/x"}`); got != `{"cmd":"cd ~\/x"}` {
		t.Errorf("got %q", got)
	}
	q := mustPaths(t, nil, PathRule{Name: "path.q", From: `/Users/a"l`, To: "~"})
	if got := q.String(`{"p":"/Users/a\"l/x"}`); got != `{"p":"~/x"}` {
		t.Errorf("got %q", got)
	}
	if got := q.String(`/Users/a"l/x`); got != `~/x` {
		t.Errorf("raw: got %q", got)
	}
}

func TestPathChunkBoundaries(t *testing.T) {
	s := mustPaths(t, nil,
		PathRule{Name: "path.home", From: "/Users/al", To: "~"},
		PathRule{Name: "path.work", From: "/Users/al/.ard/work/J", To: "/work"},
	)
	_ = s.Add("K", "s3cr3t-value-123")
	cases := []struct{ in, want string }{
		{"/Users/al" + "ice/x /Users/al" + "/x", "/Users/alice/x ~/x"},
		{"/Users/al/.ard/work/J" + "X /Users/al/.ard/work/J/f", "~/.ard/work/JX /work/f"},
		{"p /Users/al", "p ~"},
		{"/Users/al s3cr3t-value-123 /Users/al/.ard/work/J", "~ [REDACTED:K] /work"},
	}
	for _, c := range cases {
		for chunk := 1; chunk <= 7; chunk++ {
			for off := 0; off < 20; off++ {
				in := strings.Repeat("a", off) + c.in
				want, _ := s.Bytes([]byte(in))
				var out bytes.Buffer
				if _, _, err := s.stream(context.Background(), strings.NewReader(in), &out, chunk); err != nil {
					t.Fatal(err)
				}
				if out.String() != string(want) {
					t.Fatalf("chunk %d off %d: got %q want %q", chunk, off, out.String(), want)
				}
				if out.String() != strings.Repeat("a", off)+c.want {
					t.Fatalf("chunk %d off %d: got %q want %q", chunk, off, out.String(), strings.Repeat("a", off)+c.want)
				}
			}
		}
	}
}

func TestPathAndSecretTogether(t *testing.T) {
	const h = "/Users/al"
	secret := "s3cr3t-" + randStr(70, 12)
	s := NewSecrets()
	_ = s.Add("K", secret)
	s = mustPaths(t, s, PathRule{Name: "path.home", From: h, To: "~"})
	dir := t.TempDir()
	log := func(body string) (LogResult, error) {
		src := filepath.Join(dir, "raw.log")
		if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return newR(t, s, Options{}).Log(context.Background(), src, filepath.Join(dir, "out.log"))
	}
	res, err := log("cd " + h + "/src; echo " + secret + "\n" + h + "/.ard/x " + plantedTokens()["github-token"] + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Flagged {
		t.Errorf("planted token next to a path not flagged: %+v", res)
	}
	res, err = log("cd " + h + "/src; echo " + secret + "\nin " + h + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged || bytes.Contains(res.Clean, []byte(h)) || bytes.Contains(res.Clean, []byte(secret)) {
		t.Errorf("res = %+v clean = %q", res.Flagged, res.Clean)
	}
	if res.Replaced["path.home"] != 2 || res.Replaced["K"] != 1 {
		t.Errorf("replaced = %v", res.Replaced)
	}
	tr, err := newR(t, s, Options{}).Text(context.Background(), []byte("see "+h+"/a"))
	if err != nil || string(tr.Clean) != "see ~/a" {
		t.Errorf("text = %q, %v", tr.Clean, err)
	}
}
