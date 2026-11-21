package committer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx"
)

func TestCheckPath(t *testing.T) {
	tests := []struct {
		issue int
		path  string
		ok    bool
	}{
		{12, ".ard/12/issue.md", true},
		{12, ".ard/12/logs/worker-engineer-20261121.log", true},
		{12, ".ard/config.yaml", false},
		{12, ".ard/scanners/cve/scan", false},
		{12, ".ard/012/x", false},
		{12, ".ard/0/x", false},
		{12, ".ard/12", false},
		{12, ".ard/12/", false},
		{12, "./.ard/12/x", false},
		{12, ".ard/12//x", false},
		{12, ".ard/12/../13/x", false},
		{12, "/abs/.ard/12/x", false},
		{12, ".ard/13/x", false},
		{12, ".ard/12/.GIT/config", false},
		{12, ".ard/12/a\x00b", false},
		{12, ".ard/12/a\\b", false},
		{12, "", false},
		{-1, ".ard/1/x", false},
	}
	for _, tc := range tests {
		err := CheckPath(tc.issue, tc.path)
		if (err == nil) != tc.ok {
			t.Errorf("CheckPath(%d, %q) = %v, want ok=%v", tc.issue, tc.path, err, tc.ok)
		}
	}
}

func TestCheckStaged(t *testing.T) {
	decl := []string{".ard/1/a", ".ard/1/b"}
	tests := []struct {
		name string
		cs   []gitx.Change
		ok   bool
	}{
		{"empty", nil, true},
		{"subset", []gitx.Change{{Path: ".ard/1/a", NewMode: "100644", Status: 'A'}}, true},
		{"delete", []gitx.Change{{Path: ".ard/1/b", NewMode: "000000", Status: 'D'}}, true},
		{"extra", []gitx.Change{{Path: "go.mod", NewMode: "100644"}}, false},
		{"symlink", []gitx.Change{{Path: ".ard/1/a", NewMode: "120000"}}, false},
		{"gitlink", []gitx.Change{{Path: ".ard/1/a", NewMode: "160000"}}, false},
	}
	for _, tc := range tests {
		err := CheckStaged(decl, tc.cs)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrGuard)) {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestMessage(t *testing.T) {
	long := strings.Repeat("é", 100)
	tests := []struct {
		name, summary, job string
		skip               bool
		want               string
		bad                bool
	}{
		{"plain", "add issue", "", true, "ard(triage:lead): #7 add issue [skip ci]", false},
		{"no skip", "add issue", "", false, "ard(triage:lead): #7 add issue", false},
		{"forged trailer", "x\nArd-Job: X", "", false, "ard(triage:lead): #7 x Ard-Job: X", false},
		{"forged skip", "x\n[skip ci]\r\n\ty", "", false, "ard(triage:lead): #7 x [skip ci] y", false},
		{"job", "s", "01ABC", true, "ard(triage:lead): #7 s [skip ci]\n\nArd-Job: 01ABC", false},
		{"truncate", long, "", false, "ard(triage:lead): #7 " + strings.Repeat("é", 71) + "…", false},
		{"empty", " \n\t", "", true, "", true},
	}
	for _, tc := range tests {
		got, err := Message("triage", "lead", 7, tc.summary, tc.job, tc.skip)
		if tc.bad {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

func TestSnapshot(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, make([]byte, MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(dir, "ok")
	if err := os.WriteFile(ok, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	mk := func(files ...File) Request {
		return Request{Issue: 12, Stage: "triage", Role: "lead", Summary: "s", Files: files}
	}
	tests := []struct {
		name string
		req  Request
		want error
		npth int
	}{
		{"ok", mk(File{Path: ".ard/12/a", Src: ok}, File{Path: ".ard/12/b", Data: []byte("x")}, File{Path: ".ard/12/c", Delete: true}), nil, 0},
		{"three bad paths", mk(File{Path: "a", Data: []byte("x")}, File{Path: ".ard/13/a", Data: []byte("x")}, File{Path: ".ard/config.yaml", Data: []byte("x")}), ErrGuard, 3},
		{"symlink src", mk(File{Path: ".ard/12/a", Src: link}), ErrGuard, 1},
		{"too big", mk(File{Path: ".ard/12/a", Src: big}), ErrGuard, 1},
		{"too big data", mk(File{Path: ".ard/12/a", Data: make([]byte, MaxFileBytes+1)}), ErrGuard, 1},
		{"duplicate", mk(File{Path: ".ard/12/a", Data: []byte("x")}, File{Path: ".ard/12/a", Data: []byte("y")}), ErrInvalid, 0},
		{"data and delete", mk(File{Path: ".ard/12/a", Data: []byte("x"), Delete: true}), ErrInvalid, 0},
		{"no kind", mk(File{Path: ".ard/12/a"}), ErrInvalid, 0},
		{"empty", mk(), ErrInvalid, 0},
		{"missing src", mk(File{Path: ".ard/12/a", Src: filepath.Join(dir, "nope")}), ErrInvalid, 0},
	}
	for _, tc := range tests {
		out, err := snapshot(tc.req, 12)
		if tc.want == nil {
			if err != nil || string(out.Files[0].Data) != "hi" || out.Files[0].Src != "" {
				t.Errorf("%s: %v %+v", tc.name, err, out.Files)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
			continue
		}
		var ge *GuardError
		if tc.npth > 0 && (!errors.As(err, &ge) || len(ge.Paths) != tc.npth) {
			t.Errorf("%s: guard paths = %v", tc.name, err)
		}
	}
}
