package modelauth_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/redact"
)

const (
	keyVal   = "ard-fake-api-key-value-00000001"
	tokenVal = "ard-oauth-test-value-0123456789abcdef"
)

func names() func(agent.Auth) (string, error) {
	return claude.New(claude.Options{}).CredentialEnv
}

func TestResolve(t *testing.T) {
	envs := map[string]map[string]string{
		"none":       {},
		"key":        {"ANTHROPIC_API_KEY": keyVal},
		"token":      {"CLAUDE_CODE_OAUTH_TOKEN": tokenVal},
		"both":       {"ANTHROPIC_API_KEY": keyVal, "CLAUDE_CODE_OAUTH_TOKEN": tokenVal},
		"tokenempty": {"ANTHROPIC_API_KEY": keyVal, "CLAUDE_CODE_OAUTH_TOKEN": ""},
		"token7":     {"CLAUDE_CODE_OAUTH_TOKEN": "1234567"},
		"tokennl":    {"CLAUDE_CODE_OAUTH_TOKEN": tokenVal + "\n"},
		"tokenctl":   {"CLAUDE_CODE_OAUTH_TOKEN": "abcdefgh\x01ijkl"},
	}
	const key, tok = "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"
	tests := []struct {
		mode modelauth.Mode
		env  string
		name string
		auth agent.Auth
		err  error
	}{
		{"", "none", "", "", modelauth.ErrNoCredential},
		{"", "key", key, agent.AuthAPIKey, nil},
		{"", "token", tok, agent.AuthOAuthToken, nil},
		{"", "both", tok, agent.AuthOAuthToken, nil},
		{"", "tokenempty", key, agent.AuthAPIKey, nil},
		{"", "token7", "", "", modelauth.ErrBadValue},
		{"", "tokennl", "", "", modelauth.ErrBadValue},
		{"", "tokenctl", "", "", modelauth.ErrBadValue},
		{modelauth.ModeAuto, "both", tok, agent.AuthOAuthToken, nil},
		{modelauth.ModeAPIKey, "both", key, agent.AuthAPIKey, nil},
		{modelauth.ModeAPIKey, "token", "", "", modelauth.ErrNoCredential},
		{modelauth.ModeOAuthToken, "key", "", "", modelauth.ErrNoCredential},
		{modelauth.ModeOAuthToken, "both", tok, agent.AuthOAuthToken, nil},
		{modelauth.ModeOAuthToken, "tokenempty", "", "", modelauth.ErrNoCredential},
		{modelauth.ModeOAuthToken, "token7", "", "", modelauth.ErrBadValue},
		{"api_key", "both", "", "", modelauth.ErrBadMode},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%s", tt.mode, tt.env), func(t *testing.T) {
			env := envs[tt.env]
			c, err := modelauth.Resolve(tt.mode, names(), func(k string) string { return env[k] })
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("err = %v, want %v", err, tt.err)
				}
				for _, v := range env {
					if v != "" && strings.Contains(err.Error(), v) {
						t.Fatalf("error leaks value: %v", err)
					}
				}
				if _, rerr := c.Require(); !errors.Is(rerr, modelauth.ErrNoCredential) {
					t.Fatalf("Require on zero = %v", rerr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Name != tt.name || c.Auth != tt.auth {
				t.Fatalf("got %s/%s, want %s/%s", c.Name, c.Auth, tt.name, tt.auth)
			}
			want := tt.name + "=" + env[tt.name]
			if got := c.Env(); len(got) != 1 || got[0] != want {
				t.Fatalf("Env() = %v", got)
			}
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "cred", c)
			nested := struct {
				Cred  modelauth.Credential
				Other int
			}{c, 1}
			out := fmt.Sprintf("%v %+v %#v %+v %#v %s", c, c, c, nested, nested, buf.String())
			if strings.Contains(out, env[tt.name]) || !strings.Contains(out, tt.name) {
				t.Fatalf("formatting leaks or omits name: %s", out)
			}
			if _, err := c.Require(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRedactsTokenFromCommittedLog(t *testing.T) {
	c, err := modelauth.Resolve("", names(), func(k string) string {
		if k == "CLAUDE_CODE_OAUTH_TOKEN" {
			return tokenVal
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	s := redact.NewSecrets()
	if err := c.AddTo(s); err != nil {
		t.Fatal(err)
	}
	rd, err := redact.New(s, redact.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(tokenVal))
	raw := "plain: " + tokenVal + "\n" +
		`{"type":"assistant","message":"token is ` + tokenVal + `"}` + "\n" +
		"encoded: " + b64 + "\n"
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.log"), filepath.Join(dir, "dst.log")
	if err := os.WriteFile(src, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := rd.Log(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged || res.Clean == nil {
		t.Fatalf("flagged=%v findings=%v", res.Flagged, res.Findings)
	}
	got := string(res.Clean)
	for _, form := range []string{tokenVal, b64} {
		if strings.Contains(got, form) {
			t.Fatalf("committed log contains %q:\n%s", form, got)
		}
	}
	if !strings.Contains(got, "[REDACTED:CLAUDE_CODE_OAUTH_TOKEN]") {
		t.Fatalf("no marker:\n%s", got)
	}
}
