package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeToken() string { return "ghp_" + strings.Repeat("q", 36) }

// connectServer serves the endpoints Connect reads. repoCode is the status of
// GET /repos/o/r; perms are the repo permission flags; scopes the header.
func connectServer(t *testing.T, repoCode int, perms map[string]bool, scopes string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		if scopes != "" {
			w.Header().Set("X-OAuth-Scopes", scopes)
		}
		w.WriteHeader(repoCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"private": true, "permissions": perms})
	})
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })
	mux.HandleFunc("/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) })
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"login":"octo"}`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func tokCfg(srv *httptest.Server, tok string) Config {
	return Config{Repo: Repo{Owner: "o", Name: "r"}, Auth: &TokenAuth{Value: tok, Source: "env"}, BaseURL: srv.URL}
}

func wantConnectErr(t *testing.T, err error, kind ConnectKind, sub string, secrets ...string) {
	t.Helper()
	var ce *ConnectError
	if !errors.As(err, &ce) || ce.Kind != kind {
		t.Fatalf("err = %v, want kind %s", err, kind)
	}
	if !strings.Contains(ce.Error(), sub) {
		t.Errorf("err = %q, want it to contain %q", ce, sub)
	}
	if strings.Contains(ce.Error(), "\n") {
		t.Errorf("err is not one line: %q", ce)
	}
	for _, s := range secrets {
		if s != "" && strings.Contains(ce.Error(), s) {
			t.Errorf("err leaks a secret: %q", ce)
		}
	}
}

func TestConnectTokenOK(t *testing.T) {
	srv := connectServer(t, 200, map[string]bool{"push": true, "pull": true}, "repo, read:org")
	c, err := Connect(context.Background(), tokCfg(srv, fakeToken()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Login != "octo" || c.Kind != AuthToken || c.Permissions["contents"] != "write" || len(c.Scopes) != 2 {
		t.Errorf("connection = %+v", c)
	}
}

func TestConnectFineGrainedNoScopesHeader(t *testing.T) {
	srv := connectServer(t, 200, map[string]bool{"push": true}, "")
	if _, err := Connect(context.Background(), tokCfg(srv, fakeToken())); err != nil {
		t.Fatal(err)
	}
}

func TestConnectRepoNotFound(t *testing.T) {
	tok := fakeToken()
	srv := connectServer(t, 404, nil, "")
	_, err := Connect(context.Background(), tokCfg(srv, tok))
	wantConnectErr(t, err, ConnectRepoNotFound, "o/r", tok)
}

func TestConnectBadToken(t *testing.T) {
	tok := fakeToken()
	srv := connectServer(t, 401, nil, "")
	_, err := Connect(context.Background(), tokCfg(srv, tok))
	wantConnectErr(t, err, ConnectUnauthorized, "token", tok)
}

func TestConnectTokenLacksWrite(t *testing.T) {
	tok := fakeToken()
	srv := connectServer(t, 200, map[string]bool{"pull": true}, "repo")
	_, err := Connect(context.Background(), tokCfg(srv, tok))
	wantConnectErr(t, err, ConnectMissingPermission, "contents write, issues write, pull requests write", tok)
	// A dry run needs only read access.
	cfg := tokCfg(srv, tok)
	cfg.ReadOnly = true
	if _, err := Connect(context.Background(), cfg); err != nil {
		t.Errorf("read-only connect: %v", err)
	}
}

func TestConnectClassicScopeMissing(t *testing.T) {
	tok := fakeToken()
	srv := connectServer(t, 200, map[string]bool{"push": true}, "gist, read:org")
	_, err := Connect(context.Background(), tokCfg(srv, tok))
	wantConnectErr(t, err, ConnectMissingPermission, "repo scope", tok)
}

func TestConnectNoCredentials(t *testing.T) {
	_, err := Connect(context.Background(), Config{Repo: Repo{Owner: "o", Name: "r"}})
	wantConnectErr(t, err, ConnectNoCredentials, "no GitHub credentials")
}

func TestConnectCancelled(t *testing.T) {
	srv := connectServer(t, 200, map[string]bool{"push": true}, "repo")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Connect(ctx, tokCfg(srv, fakeToken())); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// appServer serves an App installation with the given permissions.
func appServer(t *testing.T, instCode int, perms map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/installation", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(instCode)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 5, "app_slug": "ard", "permissions": perms})
	})
	mux.HandleFunc("/app/installations/5/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"token":"inst-tok","expires_at":"2099-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

var fullPerms = map[string]string{"contents": "write", "issues": "write", "pull_requests": "write", "checks": "read", "statuses": "read"}

func appCfg(srv *httptest.Server, key string) Config {
	return Config{Repo: Repo{Owner: "o", Name: "r"}, Auth: &AppAuth{AppID: 1, PrivateKey: []byte(key)}, BaseURL: srv.URL}
}

func TestConnectAppOK(t *testing.T) {
	srv := appServer(t, 200, fullPerms)
	c, err := Connect(context.Background(), appCfg(srv, testKeyPEM(t)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Login != "ard[bot]" || c.Kind != AuthApp {
		t.Errorf("connection = %+v", c)
	}
}

func TestConnectAppNotInstalled(t *testing.T) {
	srv := appServer(t, 404, nil)
	_, err := Connect(context.Background(), appCfg(srv, testKeyPEM(t)))
	wantConnectErr(t, err, ConnectAppNotInstalled, "not installed on o/r")
}

func TestConnectAppMissingPermission(t *testing.T) {
	p := map[string]string{"contents": "write", "issues": "write", "pull_requests": "write", "statuses": "read"}
	srv := appServer(t, 200, p)
	_, err := Connect(context.Background(), appCfg(srv, testKeyPEM(t)))
	wantConnectErr(t, err, ConnectMissingPermission, "checks read")
}

func TestConnectBadKey(t *testing.T) {
	srv := appServer(t, 200, fullPerms)
	dashes := strings.Repeat("-", 5)
	key := dashes + "BEGIN RSA " + "PRIVATE KEY" + dashes + "\nbm90IGEga2V5\n" + dashes + "END RSA " + "PRIVATE KEY" + dashes + "\n"
	_, err := Connect(context.Background(), appCfg(srv, key))
	wantConnectErr(t, err, ConnectBadKey, "private key", "bm90IGEga2V5")
}
