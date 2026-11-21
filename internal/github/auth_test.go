package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

func envFn(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestAuthFromEnv(t *testing.T) {
	key := testKeyPEM(t)
	pkcs8 := func() string {
		k, _ := rsa.GenerateKey(rand.Reader, 2048)
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	}()
	readFile := func(name string) ([]byte, error) {
		if name == "/key.pem" {
			return []byte(key), nil
		}
		return nil, os.ErrNotExist
	}
	tests := []struct {
		name     string
		env      map[string]string
		wantKind AuthKind
		wantErr  string // substring; ErrNoCredentials handled separately
		noCreds  bool
	}{
		{name: "app with key file", env: map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_PRIVATE_KEY_FILE": "/key.pem"}, wantKind: AuthApp},
		{name: "app with inline key and installation", env: map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_INSTALLATION_ID": "34", "ARD_GITHUB_PRIVATE_KEY": key}, wantKind: AuthApp},
		{name: "inline key with escaped newlines", env: map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_PRIVATE_KEY": strings.ReplaceAll(strings.TrimSpace(key), "\n", `\n`)}, wantKind: AuthApp},
		{name: "pkcs8 key", env: map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_PRIVATE_KEY": pkcs8}, wantKind: AuthApp},
		{name: "token", env: map[string]string{"ARD_GITHUB_TOKEN": "ghp_secret"}, wantKind: AuthToken},
		{name: "nothing", env: map[string]string{}, noCreds: true},
		{name: "GH_TOKEN ignored", env: map[string]string{"GH_TOKEN": "ghp_secret"}, noCreds: true},
		{name: "app and token", env: map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_PRIVATE_KEY": key, "ARD_GITHUB_TOKEN": "ghp_secret"}, wantErr: "both"},
		{name: "non-numeric app id", env: map[string]string{"ARD_GITHUB_APP_ID": "abc", "ARD_GITHUB_PRIVATE_KEY": key}, wantErr: "ARD_GITHUB_APP_ID must"},
		{name: "non-numeric installation id", env: map[string]string{"ARD_GITHUB_APP_ID": "1", "ARD_GITHUB_INSTALLATION_ID": "x", "ARD_GITHUB_PRIVATE_KEY": key}, wantErr: "INSTALLATION_ID must"},
		{name: "both key forms", env: map[string]string{"ARD_GITHUB_APP_ID": "1", "ARD_GITHUB_PRIVATE_KEY": key, "ARD_GITHUB_PRIVATE_KEY_FILE": "/key.pem"}, wantErr: "only one"},
		{name: "app id without key", env: map[string]string{"ARD_GITHUB_APP_ID": "1"}, wantErr: "PRIVATE_KEY"},
		{name: "key without app id", env: map[string]string{"ARD_GITHUB_PRIVATE_KEY": key}, wantErr: "APP_ID is required"},
		{name: "missing key file", env: map[string]string{"ARD_GITHUB_APP_ID": "1", "ARD_GITHUB_PRIVATE_KEY_FILE": "/nope"}, wantErr: "PRIVATE_KEY_FILE"},
		{name: "garbage pem", env: map[string]string{"ARD_GITHUB_APP_ID": "1", "ARD_GITHUB_PRIVATE_KEY": "SUPERSECRETGARBAGE"}, wantErr: "not valid PEM"},
		{name: "pem but not a key", env: map[string]string{"ARD_GITHUB_APP_ID": "1", "ARD_GITHUB_PRIVATE_KEY": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("SUPERSECRETGARBAGE")}))}, wantErr: "not a parseable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := AuthFromEnv(envFn(tc.env), readFile)
			switch {
			case tc.noCreds:
				if !errors.Is(err, ErrNoCredentials) {
					t.Fatalf("err = %v, want ErrNoCredentials", err)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				for _, secret := range []string{"ghp_secret", "SUPERSECRETGARBAGE", "BEGIN RSA"} {
					if strings.Contains(err.Error(), secret) {
						t.Errorf("error leaks secret %q: %v", secret, err)
					}
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				if a.Kind() != tc.wantKind {
					t.Errorf("kind = %s", a.Kind())
				}
			}
		})
	}
}

func TestAuthFromEnvValues(t *testing.T) {
	key := testKeyPEM(t)
	a, err := AuthFromEnv(envFn(map[string]string{"ARD_GITHUB_APP_ID": "12", "ARD_GITHUB_INSTALLATION_ID": "34", "ARD_GITHUB_PRIVATE_KEY": key}), nil)
	if err != nil {
		t.Fatal(err)
	}
	app := a.(*AppAuth)
	if app.AppID != 12 || app.InstallationID != 34 {
		t.Errorf("app = %+v", app)
	}
}

func TestTokenAuthTransport(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization") }))
	defer srv.Close()
	a := NewTokenAuth("tok")
	rt, err := a.Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: rt}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "Bearer tok" {
		t.Errorf("Authorization = %q", got)
	}
}

// appStub serves the installation endpoints and an echo endpoint.
type appStub struct {
	*httptest.Server
	mints, discovers atomic.Int32
	jwtSeen          atomic.Bool
	apiAuth          atomic.Value // Authorization header seen on /echo
}

func newAppStub(t *testing.T, installID int64) *appStub {
	s := &appStub{}
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", installID), func(w http.ResponseWriter, r *http.Request) {
		s.mints.Add(1)
		s.jwtSeen.Store(strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") && strings.Count(r.Header.Get("Authorization"), ".") == 2)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"inst-token","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/o/r/installation", func(w http.ResponseWriter, r *http.Request) {
		s.discovers.Add(1)
		if strings.Count(r.Header.Get("Authorization"), ".") != 2 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"id":%d}`, installID)
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) { s.apiAuth.Store(r.Header.Get("Authorization")) })
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestAppAuthMintsAndCachesToken(t *testing.T) {
	stub := newAppStub(t, 34)
	a := &AppAuth{AppID: 12, InstallationID: 34, PrivateKey: []byte(testKeyPEM(t))}
	a.configure(Repo{"o", "r"}, stub.URL)
	rt, err := a.Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	if stub.mints.Load() != 0 {
		t.Fatal("Transport made a request")
	}
	c := &http.Client{Transport: rt}
	for i := 0; i < 2; i++ {
		resp, err := c.Get(stub.URL + "/echo")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if got := stub.apiAuth.Load(); got != "token inst-token" {
		t.Errorf("api Authorization = %v", got)
	}
	if !stub.jwtSeen.Load() {
		t.Error("token mint was not signed with a JWT bearer")
	}
	tok, err := a.Token(context.Background())
	if err != nil || tok != "inst-token" {
		t.Errorf("Token = %q, %v", tok, err)
	}
	if n := stub.mints.Load(); n != 1 {
		t.Errorf("mints = %d, want 1 (cached before expiry)", n)
	}
	if stub.discovers.Load() != 0 {
		t.Error("discovered although installation ID was set")
	}
}

func TestAppAuthDiscoversInstallation(t *testing.T) {
	stub := newAppStub(t, 77)
	a := &AppAuth{AppID: 12, PrivateKey: []byte(testKeyPEM(t))}
	a.configure(Repo{"o", "r"}, stub.URL)
	if _, err := a.Transport(nil); err != nil {
		t.Fatal(err)
	}
	tok, err := a.Token(context.Background())
	if err != nil || tok != "inst-token" {
		t.Fatalf("Token = %q, %v", tok, err)
	}
	if stub.discovers.Load() != 1 || stub.mints.Load() != 1 {
		t.Errorf("discovers=%d mints=%d", stub.discovers.Load(), stub.mints.Load())
	}
}

func TestAppAuthErrorsDoNotLeakKey(t *testing.T) {
	key := testKeyPEM(t)
	a := &AppAuth{AppID: 12, InstallationID: 1, PrivateKey: []byte(key)}
	a.configure(Repo{"o", "r"}, "http://127.0.0.1:1") // nothing listens
	if _, err := a.Transport(nil); err != nil {
		t.Fatal(err)
	}
	_, err := a.Token(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "PRIVATE KEY") || strings.Contains(err.Error(), key[40:80]) {
		t.Errorf("error leaks key: %v", err)
	}
	bad := &AppAuth{AppID: 12}
	if _, err := bad.Transport(nil); err == nil {
		t.Error("Transport without key: want error")
	}
}

func TestAppAuthDiscoveryRedirectIsRepoMoved(t *testing.T) {
	var targetHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/installation", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/repositories/7/installation?token=x")
		w.WriteHeader(http.StatusMovedPermanently)
	})
	mux.HandleFunc("/repositories/7/installation", func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		fmt.Fprint(w, `{"id":1}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a := &AppAuth{AppID: 12, PrivateKey: []byte(testKeyPEM(t))}
	a.configure(Repo{"o", "r"}, srv.URL)
	if _, err := a.Transport(nil); err != nil {
		t.Fatal(err)
	}
	_, err := a.Token(context.Background())
	var rm *RepoMovedError
	if !errors.As(err, &rm) || rm.Repo != (Repo{"o", "r"}) || !errors.Is(err, ErrRepoMoved) {
		t.Fatalf("err = %v, want *RepoMovedError for o/r", err)
	}
	if targetHits.Load() != 0 {
		t.Error("discovery followed the redirect")
	}
	if strings.Contains(err.Error(), "token=x") {
		t.Errorf("error leaks the query: %v", err)
	}
}
