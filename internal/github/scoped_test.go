package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const scopedSecretValue = "scoped-value-for-tests"

func scopedApp(t *testing.T, h http.HandlerFunc) (*AppAuth, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	a := &AppAuth{AppID: 12, InstallationID: 34, PrivateKey: []byte(testKeyPEM(t))}
	a.configure(Repo{"o", "r"}, srv.URL)
	return a, &n
}

func TestMintReadToken(t *testing.T) {
	tests := []struct {
		name     string
		repos    []Repo
		handler  http.HandlerFunc
		wantErr  error
		anyErr   bool
		requests int32
	}{
		{name: "created", repos: []Repo{{"o", "r"}, {"o", "side"}}, requests: 1, handler: func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/app/installations/34/access_tokens" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if strings.Count(r.Header.Get("Authorization"), ".") != 2 {
				t.Errorf("not a JWT: %q", r.Header.Get("Authorization"))
			}
			var body struct {
				Repositories []string          `json:"repositories"`
				Permissions  map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if fmt.Sprint(body.Repositories) != "[r side]" || len(body.Permissions) != 1 || body.Permissions["contents"] != "read" {
				t.Errorf("body = %+v", body)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":%q,"expires_at":"2030-01-01T00:00:00Z"}`, scopedSecretValue)
		}},
		{name: "422", repos: []Repo{{"o", "r"}}, requests: 1, wantErr: ErrNotInInstallation, handler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"repo not accessible"}`)
		}},
		{name: "other owner", repos: []Repo{{"x", "r"}}, requests: 0, wantErr: ErrNotInInstallation, handler: func(w http.ResponseWriter, r *http.Request) {}},
		{name: "redirect", repos: []Repo{{"o", "r"}}, requests: 1, anyErr: true, handler: func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		}},
		{name: "500", repos: []Repo{{"o", "r"}}, requests: 1, anyErr: true, handler: func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, n := scopedApp(t, tc.handler)
			tok, err := a.MintReadToken(context.Background(), tc.repos)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.anyErr:
				if err == nil {
					t.Error("want error")
				}
			default:
				if err != nil || tok.Value != scopedSecretValue || tok.ExpiresAt.Year() != 2030 {
					t.Errorf("tok = %+v, %v", tok, err)
				}
			}
			if got := n.Load(); got != tc.requests {
				t.Errorf("requests = %d, want %d", got, tc.requests)
			}
		})
	}
}

func TestRevokeToken(t *testing.T) {
	for code, wantErr := range map[int]bool{204: false, 401: false, 500: true} {
		a, _ := scopedApp(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete || r.URL.Path != "/installation/token" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+scopedSecretValue {
				t.Errorf("Authorization = %q", got)
			}
			w.WriteHeader(code)
		})
		if err := a.RevokeToken(context.Background(), scopedSecretValue); (err != nil) != wantErr {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
}

func TestScopedMinterRefusesPAT(t *testing.T) {
	if _, err := ScopedMinter(NewTokenAuth("x")); !errors.Is(err, ErrCannotScope) {
		t.Errorf("err = %v", err)
	}
	app := &AppAuth{AppID: 1}
	if got, err := ScopedMinter(app); err != nil || got != app {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestScopedTokenNotLogged(t *testing.T) {
	tok := ScopedToken{Value: scopedSecretValue}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("minted", "token", tok)
	out := buf.String() + fmt.Sprintf("%v %s %+v %#v", tok, tok, tok, tok)
	if strings.Contains(out, scopedSecretValue) {
		t.Errorf("value leaked: %s", out)
	}
}
