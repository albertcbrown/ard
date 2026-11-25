package setupcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/github"
)

// testPEM is built at run time so no key-shaped literal is in the source.
var testPEM = "-----BEGIN " + "RSA PRIVATE KEY-----\nQUJDREVGRw==\n-----END " + "RSA PRIVATE KEY-----\n"

func TestManifestPermissionsMatchBuiltIn(t *testing.T) {
	m, err := BuildManifest("n", "http://127.0.0.1:1/callback")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.DefaultPermissions, AppPermissions) {
		t.Errorf("permissions = %v", m.DefaultPermissions)
	}
	if miss, extra := CheckPermissions(m.DefaultPermissions); len(miss)+len(extra) != 0 {
		t.Errorf("check disagrees: missing %v extra %v", miss, extra)
	}
	for _, f := range ForbiddenPermissions {
		if _, ok := m.DefaultPermissions[f]; ok {
			t.Errorf("%s requested", f)
		}
	}
	b, _ := json.Marshal(m)
	if m.Public || strings.Contains(string(b), "hook_attributes") {
		t.Errorf("manifest must be private with no webhook: %s", b)
	}
	miss, extra := CheckPermissions(map[string]string{"contents": "read", "workflows": "write"})
	if len(miss) == 0 || len(extra) != 1 {
		t.Errorf("missing %v extra %v", miss, extra)
	}
}

func TestManifestRejectsForbidden(t *testing.T) {
	for _, f := range ForbiddenPermissions {
		p := map[string]string{f: "write"}
		if _, err := buildManifest(p, "n", "u"); err == nil {
			t.Errorf("%s accepted", f)
		}
	}
}

func TestManifestURLUserVsOrg(t *testing.T) {
	u := ManifestURL("me", false, "st")
	if u != "https://github.com/settings/apps/new?state=st" {
		t.Errorf("user url %s", u)
	}
	o := ManifestURL("acme", true, "st")
	if o != "https://github.com/organizations/acme/settings/apps/new?state=st" {
		t.Errorf("org url %s", o)
	}
}

func startCallback(t *testing.T, ctx context.Context, timeout time.Duration) (base string, res chan [2]string) {
	t.Helper()
	baseCh := make(chan string, 1)
	res = make(chan [2]string, 1)
	render := func(string) ([]byte, string) {
		return []byte(`{"a":"<b>"}`), "https://github.com/settings/apps/new?state=s"
	}
	go func() {
		code, err := serveCallback(ctx, render, "s", timeout, func(u string) { baseCh <- u })
		e := ""
		if err != nil {
			e = err.Error()
		}
		res <- [2]string{code, e}
	}()
	select {
	case base = <-baseCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}
	return base, res
}

func TestCallbackLoopbackStateAndSingleUse(t *testing.T) {
	base, res := startCallback(t, context.Background(), time.Minute)
	u, _ := url.Parse(base)
	if !strings.HasPrefix(u.Host, "127.0.0.1:") {
		t.Fatalf("host %s", u.Host)
	}
	page, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	var pb bytes.Buffer
	_, _ = pb.ReadFrom(page.Body)
	page.Body.Close()
	if !strings.Contains(pb.String(), "&lt;b&gt;") || strings.Contains(pb.String(), "<b>") {
		t.Errorf("manifest not escaped: %s", pb.String())
	}
	r, err := http.Get(base + "callback?code=c1&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong state status %d", r.StatusCode)
	}
	r, err = http.Get(base + "callback?code=c1&state=s")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Errorf("status %d", r.StatusCode)
	}
	if got := <-res; got[0] != "c1" || got[1] != "" {
		t.Errorf("result %v", got)
	}
	c := http.Client{Timeout: time.Second}
	if r, err := c.Get(base + "callback?code=c2&state=s"); err == nil {
		r.Body.Close()
		t.Error("server still accepting after the first valid request")
	}
}

func TestCallbackTimeoutAndCancel(t *testing.T) {
	base, res := startCallback(t, context.Background(), 50*time.Millisecond)
	if got := <-res; got[1] == "" {
		t.Error("want timeout error")
	}
	c := http.Client{Timeout: time.Second}
	if r, err := c.Get(base); err == nil {
		r.Body.Close()
		t.Error("listener open after timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, res = startCallback(t, ctx, time.Minute)
	cancel()
	if got := <-res; got[1] == "" {
		t.Error("want cancel error")
	}
}

func fakeAPI(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.PathValue("code") != "thecode" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("GET /users/{o}", func(w http.ResponseWriter, r *http.Request) {
		typ := "User"
		if r.PathValue("o") == "acme" {
			typ = "Organization"
		}
		_, _ = fmt.Fprintf(w, `{"type":%q}`, typ)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func okBody() string {
	b, _ := json.Marshal(map[string]any{"id": 42, "slug": "ard-x", "pem": testPEM, "client_secret": "s3cret"})
	return string(b)
}

func TestConvert(t *testing.T) {
	s := fakeAPI(t, 201, okBody())
	c, err := Convert(context.Background(), s.URL, nil, "thecode")
	if err != nil || c.ID != 42 || c.Slug != "ard-x" || c.PEM != testPEM {
		t.Fatalf("%v %+v", err, c)
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "c", c)
	all := fmt.Sprintf("%v %+v %#v %s", c, c, c, c) + buf.String()
	if strings.Contains(all, "QUJDREVGRw") || strings.Contains(all, "PRIVATE KEY") {
		t.Errorf("key leaked: %s", all)
	}
	for _, st := range []int{404, 422} {
		s := fakeAPI(t, st, `{"message":"bad","echo":"`+strings.ReplaceAll(testPEM, "\n", `\n`)+`"}`)
		_, err := Convert(context.Background(), s.URL, nil, "thecode")
		if err == nil || strings.Contains(err.Error(), "QUJDREVGRw") || !strings.Contains(err.Error(), fmt.Sprint(st)) {
			t.Errorf("status %d err %v", st, err)
		}
	}
}

func TestWriteKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "keys", "a.pem")
	if err := WriteKey(p, []byte("one"), false); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
	}
	if err := WriteKey(p, []byte("two"), false); err == nil {
		t.Error("overwrote without force")
	}
	if err := CheckKeyPath(p, false); err == nil {
		t.Error("CheckKeyPath accepted existing path")
	}
	if b, _ := os.ReadFile(p); string(b) != "one" {
		t.Errorf("contents %q", b)
	}
	if err := WriteKey(p, []byte("two"), true); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(p)
	if b, _ := os.ReadFile(p); string(b) != "two" || fi.Mode().Perm() != 0o600 {
		t.Errorf("force: %q %v", b, fi.Mode().Perm())
	}
}

func testEnv(dry bool) (*cli.Env, *bytes.Buffer) {
	var out bytes.Buffer
	return &cli.Env{Globals: cli.Globals{DryRun: dry}, Log: quiet, Stdout: &out, Stderr: &out}, &out
}

func TestCreateAppDryRun(t *testing.T) {
	s := fakeAPI(t, 201, okBody())
	e, out := testEnv(true)
	keyPath := filepath.Join(t.TempDir(), "k.pem")
	opened := false
	err := createApp(context.Background(), e, github.Repo{Owner: "acme", Name: "r"}, CreateAppOpts{KeyPath: keyPath},
		createDeps{apiBase: s.URL, openBrowser: func(string) error { opened = true; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if opened || !strings.Contains(out.String(), "/organizations/acme/settings/apps/new") || !strings.Contains(out.String(), `"default_permissions"`) {
		t.Errorf("output: %s", out.String())
	}
	if _, err := os.Stat(keyPath); err == nil {
		t.Error("key written in dry run")
	}
}

func TestCreateAppUserAndOrg(t *testing.T) {
	for _, tc := range []struct{ owner, wantHost string }{
		{"me", "https://github.com/settings/apps/new"},
		{"acme", "https://github.com/organizations/acme/settings/apps/new"},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			s := fakeAPI(t, 201, okBody())
			keyPath := filepath.Join(t.TempDir(), "k.pem")
			e, out := testEnv(false)
			for _, k := range []string{"ARD_GITHUB_APP_ID", "ARD_GITHUB_INSTALLATION_ID", "ARD_GITHUB_PRIVATE_KEY_FILE"} {
				t.Setenv(k, "")
			}
			var form string
			d := createDeps{
				apiBase: s.URL,
				openBrowser: func(local string) error {
					resp, err := http.Get(local)
					if err != nil {
						return err
					}
					var b bytes.Buffer
					_, _ = b.ReadFrom(resp.Body)
					resp.Body.Close()
					form = b.String()
					// GitHub would redirect back with the state it was given.
					i := strings.Index(form, "state=")
					st := form[i+len("state=") : i+len("state=")+32]
					r, err := http.Get(local + "callback?code=thecode&state=" + st)
					if err == nil {
						r.Body.Close()
					}
					return err
				},
				discover: func(_ context.Context, id int64, pem []byte, _ github.Repo) (int64, error) {
					if id != 42 || string(pem) != testPEM {
						return 0, fmt.Errorf("bad creds")
					}
					return 7, nil
				},
				pollEvery: time.Millisecond,
			}
			if err := createApp(context.Background(), e, github.Repo{Owner: tc.owner, Name: "r"}, CreateAppOpts{KeyPath: keyPath}, d); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(form, tc.wantHost+"?state=") {
				t.Errorf("form posts elsewhere: %s", form)
			}
			for _, w := range []string{"ARD_GITHUB_APP_ID=42", "ARD_GITHUB_INSTALLATION_ID=7", "ARD_GITHUB_PRIVATE_KEY_FILE=" + keyPath, "/apps/ard-x/installations/new"} {
				if !strings.Contains(out.String(), w) {
					t.Errorf("missing %q in %s", w, out.String())
				}
			}
			if strings.Contains(out.String(), "QUJDREVGRw") || strings.Contains(out.String(), "s3cret") {
				t.Errorf("secret printed: %s", out.String())
			}
			if b, _ := os.ReadFile(keyPath); string(b) != testPEM {
				t.Error("key not stored")
			}
			if os.Getenv("ARD_GITHUB_APP_ID") != "42" {
				t.Error("env not set")
			}
		})
	}
}

func TestCreateAppInstallTimeoutAndBrowserFailure(t *testing.T) {
	s := fakeAPI(t, 201, okBody())
	e, out := testEnv(false)
	d := createDeps{
		apiBase: s.URL,
		openBrowser: func(local string) error {
			go func() {
				resp, _ := http.Get(local)
				b := new(bytes.Buffer)
				_, _ = b.ReadFrom(resp.Body)
				resp.Body.Close()
				st := b.String()[strings.Index(b.String(), "state=")+6:][:32]
				r, _ := http.Get(local + "callback?code=thecode&state=" + st)
				r.Body.Close()
			}()
			return fmt.Errorf("no browser")
		},
		discover:  func(context.Context, int64, []byte, github.Repo) (int64, error) { return 0, fmt.Errorf("404") },
		pollEvery: time.Millisecond,
		pollFor:   30 * time.Millisecond,
	}
	keyPath := filepath.Join(t.TempDir(), "k.pem")
	err := createApp(context.Background(), e, github.Repo{Owner: "me", Name: "r"}, CreateAppOpts{KeyPath: keyPath}, d)
	if err == nil || !strings.Contains(err.Error(), "installed") {
		t.Errorf("err %v", err)
	}
	if !strings.Contains(out.String(), "open the URL above") || !strings.Contains(out.String(), "rerun `ard setup`") {
		t.Errorf("output %s", out.String())
	}
	if strings.Contains(out.String(), "QUJDREVGRw") {
		t.Error("key printed")
	}
}

func TestCreateAppRefusesExistingKeyBeforeBrowser(t *testing.T) {
	s := fakeAPI(t, 201, okBody())
	keyPath := filepath.Join(t.TempDir(), "k.pem")
	_ = os.WriteFile(keyPath, []byte("x"), 0o600)
	e, _ := testEnv(false)
	opened := false
	err := createApp(context.Background(), e, github.Repo{Owner: "me", Name: "r"}, CreateAppOpts{KeyPath: keyPath},
		createDeps{apiBase: s.URL, openBrowser: func(string) error { opened = true; return nil }})
	if err == nil || opened {
		t.Errorf("err %v opened %v", err, opened)
	}
}
