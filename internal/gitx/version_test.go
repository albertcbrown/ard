package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	ok := map[string]Version{
		"git version 2.39.5\n":               {2, 39, 5},
		"git version 2.50.1 (Apple Git-155)": {2, 50, 1},
		"git version 2.47":                   {2, 47, 0},
		"git version 2.48.0.rc1":             {2, 48, 0},
		"git version 2.51.0.windows.1":       {2, 51, 0},
	}
	for in, want := range ok {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "garbage", "git version x.y"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q): want error", in)
		}
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b Version
		want bool
	}{
		{Version{1, 9, 9}, Version{2, 0, 0}, true},
		{Version{2, 39, 5}, Version{2, 47, 0}, true},
		{Version{2, 46, 9}, Version{2, 47, 0}, true},
		{Version{2, 47, 0}, Version{2, 47, 0}, false},
		{Version{2, 47, 1}, Version{2, 47, 0}, false},
		{Version{3, 0, 0}, Version{2, 99, 99}, false},
	}
	for _, c := range cases {
		if got := c.a.Less(c.b); got != c.want {
			t.Errorf("%v.Less(%v) = %v", c.a, c.b, got)
		}
	}
}

func shim(t *testing.T, out string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckVersion(t *testing.T) {
	ctx := context.Background()
	err := New(shim(t, "git version 2.39.5")).CheckVersion(ctx)
	if err == nil || !strings.Contains(err.Error(), "2.39.5") || !strings.Contains(err.Error(), "2.47.0") {
		t.Errorf("old git: got %v", err)
	}
	if err := New(shim(t, "git version 2.46.9")).CheckVersion(ctx); err == nil {
		t.Error("2.46.9 should be rejected")
	}
	for _, v := range []string{"2.47.0", "2.56.1"} {
		if err := New(shim(t, "git version "+v)).CheckVersion(ctx); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	if err := New(shim(t, "junk")).CheckVersion(ctx); err == nil {
		t.Error("junk: want error")
	}
	if err := New(filepath.Join(t.TempDir(), "nope")).CheckVersion(ctx); err == nil {
		t.Error("missing binary: want error")
	}
}
