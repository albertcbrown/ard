package github

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGh writes a gh script into a new directory and returns the directory.
// The script logs each invocation to calls and behaves as logged in or out.
func fakeGh(t *testing.T, loggedIn bool, tokenOut string) (dir, calls string) {
	t.Helper()
	dir = t.TempDir()
	calls = filepath.Join(t.TempDir(), "calls")
	status := "exit 1"
	if loggedIn {
		status = "exit 0"
	}
	script := "#!/bin/sh\necho \"$@\" >> '" + calls + "'\n" +
		"case \"$1 $2\" in\n\"auth status\") echo 'secret-in-stderr-" + tokenOut + "' >&2; " + status + " ;;\n" +
		"\"auth token\") printf '" + tokenOut + "\\n' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func TestGhFallbackAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := GhFallback(context.Background(), exec.LookPath, ExecRun, &bytes.Buffer{})
	if !errors.Is(err, ErrNoGh) {
		t.Fatalf("err = %v, want ErrNoGh", err)
	}
	for _, s := range []string{"ARD_GITHUB_APP_ID", "ARD_GITHUB_TOKEN"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q does not name %s", err, s)
		}
	}
}

func TestGhFallbackLoggedOut(t *testing.T) {
	dir, _ := fakeGh(t, false, "x")
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	_, err := GhFallback(context.Background(), exec.LookPath, ExecRun, &bytes.Buffer{})
	if !errors.Is(err, ErrGhNotLoggedIn) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "gh auth login --hostname github.com --git-protocol https --scopes repo") {
		t.Errorf("error %q lacks the login command", err)
	}
}

func TestGhFallbackLoggedIn(t *testing.T) {
	tok := "gho_" + strings.Repeat("z", 36)
	dir, calls := fakeGh(t, true, tok)
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	var out bytes.Buffer
	a, err := GhFallback(context.Background(), exec.LookPath, ExecRun, &out)
	if err != nil {
		t.Fatal(err)
	}
	if a.Value != tok || a.Source != "gh" {
		t.Errorf("auth = %v", a)
	}
	if !strings.Contains(out.String(), "gh auth token") || strings.Contains(out.String(), tok) {
		t.Errorf("output = %q", out.String())
	}
	b, _ := os.ReadFile(calls)
	if n := strings.Count(string(b), "auth token"); n != 1 {
		t.Errorf("gh auth token ran %d times: %q", n, b)
	}
}

func TestGhTokenEmpty(t *testing.T) {
	look := func(string) (string, error) { return "gh", nil }
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "token" {
			return []byte("  \n"), nil
		}
		return nil, nil
	}
	if _, err := GhFallback(context.Background(), look, run, &bytes.Buffer{}); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestGhTokenTrimmedAndErrorsDontLeak(t *testing.T) {
	look := func(string) (string, error) { return "gh", nil }
	tok := "gho_" + strings.Repeat("y", 36)
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "token" {
			return []byte("\t" + tok + " \r\n"), nil
		}
		return nil, nil
	}
	a, err := GhFallback(context.Background(), look, run, &bytes.Buffer{})
	if err != nil || a.Value != tok {
		t.Fatalf("auth = %v, err = %v", a, err)
	}
}
