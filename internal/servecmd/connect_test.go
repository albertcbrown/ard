package servecmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/github"
)

func ghServer(t *testing.T, repoCode int, push bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "repo")
		w.WriteHeader(repoCode)
		if push {
			_, _ = w.Write([]byte(`{"private":true,"permissions":{"push":true,"pull":true}}`))
		} else {
			_, _ = w.Write([]byte(`{"private":true,"permissions":{"pull":true}}`))
		}
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"login":"octo","id":7}`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// connEnv sets up an environment with no GitHub credentials and returns an Env
// whose log and stderr are captured.
func connEnv(t *testing.T, srv *httptest.Server) (*cli.Env, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	for _, n := range github.EnvNames {
		t.Setenv(n, "")
	}
	old := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	t.Cleanup(func() { githubAPIBaseURL = old })
	var logs, errs bytes.Buffer
	e := &cli.Env{Log: slog.New(slog.NewTextHandler(&logs, nil)), Stdout: io.Discard, Stderr: &errs}
	return e, &logs, &errs
}

func fakeGhOnPath(t *testing.T, loggedIn bool, tok string) {
	t.Helper()
	dir := t.TempDir()
	status := "exit 1"
	if loggedIn {
		status = "exit 0"
	}
	script := "#!/bin/sh\ncase \"$1 $2\" in\n\"auth status\") " + status + " ;;\n\"auth token\") echo '" + tok + "' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
}

func TestBuildDepsTokenLive(t *testing.T) {
	srv := ghServer(t, 200, true)
	e, logs, _ := connEnv(t, srv)
	tok := "ghp_" + strings.Repeat("a", 36)
	t.Setenv("ARD_GITHUB_TOKEN", tok)
	d, err := buildDeps(context.Background(), e, github.Repo{Owner: "o", Name: "r"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d.Close != nil {
		defer d.Close()
	}
	if n := strings.Count(logs.String(), "github.connect"); n != 1 {
		t.Errorf("github.connect logged %d times: %s", n, logs)
	}
	if !strings.Contains(logs.String(), "login=octo") || strings.Contains(logs.String(), tok) {
		t.Errorf("logs = %s", logs)
	}
}

func TestBuildDepsConnectFailures(t *testing.T) {
	tests := []struct {
		name string
		code int
		push bool
		want string
	}{
		{"repo not found", 404, true, "not found"},
		{"missing permission", 200, false, "contents write"},
		{"bad token", 401, true, "rejected the token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := ghServer(t, tc.code, tc.push)
			e, logs, _ := connEnv(t, srv)
			tok := "ghp_" + strings.Repeat("b", 36)
			t.Setenv("ARD_GITHUB_TOKEN", tok)
			_, err := buildDeps(context.Background(), e, github.Repo{Owner: "o", Name: "r"}, t.TempDir())
			var ee *cli.ExitError
			if !errors.As(err, &ee) || ee.Code != 1 {
				t.Fatalf("err = %v, want exit code 1", err)
			}
			if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), "\n") {
				t.Errorf("err = %q", err)
			}
			if strings.Contains(logs.String(), "github.connect") {
				t.Errorf("connect logged on failure")
			}
		})
	}
}

func TestServeExitsBeforeLoopsOnConnectFailure(t *testing.T) {
	srv := ghServer(t, 404, true)
	e, logs, _ := connEnv(t, srv)
	t.Setenv("ARD_GITHUB_TOKEN", "ghp_"+strings.Repeat("c", 36))
	cfg, _ := repoWithRemote(t, "max_pods: 2\n")
	e.Globals = cli.Globals{Repo: "o/r", ConfigPath: cfg}
	err := serve(context.Background(), e, buildDeps)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(logs.String(), "serve.start") || strings.Contains(logs.String(), "serve.loops") {
		t.Errorf("loops started: %s", logs)
	}
}

func TestBuildDepsGhStates(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		e, _, _ := connEnv(t, ghServer(t, 200, true))
		t.Setenv("PATH", t.TempDir())
		_, err := buildDeps(context.Background(), e, github.Repo{Owner: "o", Name: "r"}, t.TempDir())
		if !errors.Is(err, github.ErrNoGh) || !strings.Contains(err.Error(), "ARD_GITHUB_TOKEN") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("logged out", func(t *testing.T) {
		e, _, _ := connEnv(t, ghServer(t, 200, true))
		fakeGhOnPath(t, false, "x")
		_, err := buildDeps(context.Background(), e, github.Repo{Owner: "o", Name: "r"}, t.TempDir())
		if !errors.Is(err, github.ErrGhNotLoggedIn) || !strings.Contains(err.Error(), github.GhLoginCommand) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestGhTokenNotLeaked(t *testing.T) {
	srv := ghServer(t, 200, true)
	e, logs, errs := connEnv(t, srv)
	tok := "gho_" + strings.Repeat("m", 36)
	fakeGhOnPath(t, true, tok)
	stateDir := t.TempDir()
	d, err := buildDeps(context.Background(), e, github.Repo{Owner: "o", Name: "r"}, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Close != nil {
		defer d.Close()
	}
	if !strings.Contains(errs.String(), "gh auth token") {
		t.Errorf("stderr does not say gh is used: %q", errs)
	}
	for name, out := range map[string]string{"logs": logs.String(), "stderr": errs.String()} {
		if strings.Contains(out, tok) {
			t.Errorf("%s leak the gh token", name)
		}
	}
	// The token reaches git push only, through deps.Token; it is not in the environment.
	if got, _ := d.Token(context.Background()); got != tok {
		t.Errorf("deps.Token = %q", got)
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, tok) {
			t.Errorf("token in the environment")
		}
	}
	_ = filepath.WalkDir(stateDir, func(p string, de os.DirEntry, _ error) error {
		if de != nil && !de.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(tok)) {
				t.Errorf("token written to %s", p)
			}
		}
		return nil
	})
	// The redactor masks it in text.
	red, err := newRedactor(&github.TokenAuth{Value: tok, Source: "gh"})
	if err != nil {
		t.Fatal(err)
	}
	out := redactText(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), red, "header "+tok+" end")
	if strings.Contains(out, tok) {
		t.Errorf("redactor kept the token: %q", out)
	}
}
