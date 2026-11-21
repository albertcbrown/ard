package redact

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestSecretsVariants(t *testing.T) {
	pem := "-----BEGIN TEST-----\n" + randStr(20, 40) + "\n" + randStr(21, 40) + "\r\n-----END TEST-----"
	jsonQ := "pa\"ss/wo+rd=" + randStr(22, 20)
	tests := map[string]string{
		"plain":  randStr(23, 24),
		"slashy": jsonQ,
		"pem":    pem,
	}
	for name, val := range tests {
		t.Run(name, func(t *testing.T) {
			s := NewSecrets()
			if err := s.Add("KEY", val); err != nil {
				t.Fatal(err)
			}
			b := []byte(val)
			j, _ := json.Marshal(val)
			vs := []string{
				val,
				base64.StdEncoding.EncodeToString(b), base64.URLEncoding.EncodeToString(b),
				base64.RawStdEncoding.EncodeToString(b), base64.RawURLEncoding.EncodeToString(b),
				url.QueryEscape(val), url.PathEscape(val), string(j[1 : len(j)-1]),
			}
			if name == "pem" {
				vs = append(vs, strings.Split(strings.ReplaceAll(val, "\r", ""), "\n")[1], strings.Split(val, "\n")[2][:40])
			}
			for _, v := range vs {
				out, counts := s.Bytes([]byte("before " + v + " after"))
				if bytes.Contains(out, []byte(v)) || !bytes.Contains(out, []byte("[REDACTED:KEY]")) {
					t.Errorf("variant %q not redacted: %q", v, out)
				}
				if counts["KEY"] != 1 {
					t.Errorf("variant %q counts = %v", v, counts)
				}
			}
		})
	}
}

func TestSecretsAddErrors(t *testing.T) {
	s := NewSecrets()
	for _, n := range []string{"", "has space", strings.Repeat("a", 65), "a/b"} {
		if err := s.Add(n, "longenoughvalue"); !errors.Is(err, ErrBadName) {
			t.Errorf("Add(%q) err = %v", n, err)
		}
	}
}

func TestShortValueSkippedAndLoggedByName(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	s := NewSecrets()
	if err := s.Add("SHORTKEY", "abc1234"); err != nil {
		t.Fatal(err)
	}
	if got := s.String("x abc1234 y"); got != "x abc1234 y" {
		t.Errorf("short value redacted: %q", got)
	}
	if !strings.Contains(buf.String(), "SHORTKEY") || strings.Contains(buf.String(), "abc1234") {
		t.Errorf("warning = %q", buf.String())
	}
}

func TestEmptySetPassesThrough(t *testing.T) {
	in := []byte("a\x00b\xff\xfe\r\nc")
	out, counts := NewSecrets().Bytes(in)
	if !bytes.Equal(in, out) || len(counts) != 0 {
		t.Errorf("out = %q counts = %v", out, counts)
	}
}

func TestChunkBoundaries(t *testing.T) {
	secret := "s3cr3t-" + randStr(30, 12)
	s := NewSecrets()
	if err := s.Add("K", secret); err != nil {
		t.Fatal(err)
	}
	for chunk := 1; chunk <= 7; chunk++ {
		for off := 0; off < 20; off++ {
			in := strings.Repeat("a", off) + secret + "tail" + secret
			var out bytes.Buffer
			if _, _, err := s.stream(context.Background(), strings.NewReader(in), &out, chunk); err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(in, secret, "[REDACTED:K]")
			if out.String() != want {
				t.Fatalf("chunk %d off %d: got %q want %q", chunk, off, out.String(), want)
			}
		}
	}
}

func TestPrefixOverlapLongestWins(t *testing.T) {
	a := "prefixvalue-" + randStr(31, 6)
	b := a + randStr(32, 10)
	s := NewSecrets()
	_ = s.Add("A", a)
	_ = s.Add("B", b)
	for chunk := 1; chunk <= 7; chunk++ {
		var out bytes.Buffer
		if _, _, err := s.stream(context.Background(), strings.NewReader("x"+b+"y"), &out, chunk); err != nil {
			t.Fatal(err)
		}
		if out.String() != "x[REDACTED:B]y" {
			t.Fatalf("chunk %d: %q", chunk, out.String())
		}
	}
}

func TestFromEnvironAndAddEnvAndMerge(t *testing.T) {
	tok, key, path := randStr(40, 20), randStr(41, 20), "/etc/keys/"+randStr(42, 10)
	s := FromEnviron([]string{
		"GH_TOKEN=" + tok, "ARD_GITHUB_PRIVATE_KEY=" + key,
		"ARD_GITHUB_PRIVATE_KEY_FILE=" + path, "HOME=/Users/someone-long-name", "BAD KEY=ignored-ignored",
	})
	out := s.String(strings.Join([]string{tok, key, path, "/Users/someone-long-name"}, " "))
	if strings.Contains(out, tok) || strings.Contains(out, key) {
		t.Errorf("credentials not redacted: %q", out)
	}
	if !strings.Contains(out, path) || !strings.Contains(out, "/Users/someone-long-name") {
		t.Errorf("non-credential redacted: %q", out)
	}
	model := randStr(43, 30)
	e := NewSecrets()
	if err := e.AddEnv(map[string]string{"ANYTHING": model}); err != nil {
		t.Fatal(err)
	}
	m := e.Merge(s).Merge(nil)
	if got := m.String(model + tok); got != "[REDACTED:ANYTHING][REDACTED:GH_TOKEN]" {
		t.Errorf("merged = %q", got)
	}
	if err := e.AddEnv(map[string]string{"bad name": model}); !errors.Is(err, ErrBadName) {
		t.Errorf("err = %v", err)
	}
}
