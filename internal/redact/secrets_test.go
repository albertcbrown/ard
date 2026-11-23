package redact

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSecretsVariants(t *testing.T) {
	pem := "-----BEGIN TEST-----\n" + randStr(20, 40) + "\n" + randStr(21, 40) + "\r\n-----END TEST-----"
	jsonQ := "pa\"ss/wo+rd=" + randStr(22, 20)
	tests := map[string]string{
		"html":   "ab<cd>ef&gh\"ij",
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
			var jb bytes.Buffer
			enc := json.NewEncoder(&jb)
			enc.SetEscapeHTML(false)
			_ = enc.Encode(val)
			jsq := strings.TrimSuffix(jb.String(), "\n")
			vs := []string{
				jsq[1 : len(jsq)-1],
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
	pm, err := m.WithPaths(PathRule{Name: "path.home", From: "/Users/al", To: "~"})
	if err != nil {
		t.Fatal(err)
	}
	if got := pm.Merge(nil).Merge(e).String("/Users/al/x " + model); got != "~/x [REDACTED:ANYTHING]" {
		t.Errorf("merge dropped a path rule: %q", got)
	}
}

func TestOverlappingSecretsRedactedAsOneSpan(t *testing.T) {
	s := NewSecrets()
	_ = s.Add("A", "aaaaaaaabbbb")
	_ = s.Add("B", "bbbbcccc")
	for chunk := 1; chunk <= 7; chunk++ {
		for off := 0; off < 14; off++ {
			in := strings.Repeat("x", off) + "aaaaaaaabbbbcccc" + "yy"
			var out bytes.Buffer
			if _, _, err := s.stream(context.Background(), strings.NewReader(in), &out, chunk); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "cccc") || strings.Contains(out.String(), "bbbb") || !strings.HasSuffix(out.String(), "yy") {
				t.Fatalf("chunk %d off %d: %q", chunk, off, out.String())
			}
		}
	}
}

func TestSelfOverlappingSecretBoundedAndLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("large input skipped in -short mode")
	}
	s := NewSecrets()
	_ = s.Add("X", "xxxxxxxx")
	in := io.LimitReader(repeatReader('x'), 64<<20)
	var out bytes.Buffer
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	counts, n, err := s.stream(context.Background(), in, &out, defaultChunk)
	el := time.Since(start)
	runtime.ReadMemStats(&after)
	if err != nil || n != 64<<20 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	if out.String() != "[REDACTED:X]" || counts["X"] != 1 {
		t.Fatalf("out = %.60q counts = %v", out.String(), counts)
	}
	d := after.TotalAlloc - before.TotalAlloc
	t.Logf("elapsed %v, TotalAlloc %d KiB", el, d>>10)
	if d > 16<<20 || (el > 5*time.Second && !raceEnabled) {
		t.Errorf("alloc %d MiB, time %v", d>>20, el)
	}
}

type repeatReader byte

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func TestSecretsNames(t *testing.T) {
	a, b := randStr(31, 24), randStr(32, 24)
	s := NewSecrets()
	for name, v := range map[string]string{"TOKEN_A": a, "TOKEN_B": b, "TOKEN_C": randStr(33, 24)} {
		if err := s.Add(name, v); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"clean":       {"nothing to see here", nil},
		"empty":       {"", nil},
		"raw":         {"x = \"" + a + "\"", []string{"TOKEN_A"}},
		"base64":      {base64.StdEncoding.EncodeToString([]byte(b)), []string{"TOKEN_B"}},
		"two sorted":  {b + "\n" + a + "\n" + a, []string{"TOKEN_A", "TOKEN_B"}},
		"urlencoded":  {url.QueryEscape(a), []string{"TOKEN_A"}},
		"near-prefix": {a[:20], nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := s.Names([]byte(tc.in))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("Names = %v, want %v", got, tc.want)
			}
			for _, n := range got {
				if strings.Contains(n, a) || strings.Contains(n, b) {
					t.Error("a name contains a value")
				}
			}
		})
	}
	ps, err := s.WithPaths(PathRule{Name: "path.home", From: "/Users/al", To: "~"})
	if err != nil {
		t.Fatal(err)
	}
	if got := ps.Names([]byte("/Users/al/x " + a)); strings.Join(got, ",") != "TOKEN_A" {
		t.Errorf("Names with a path rule = %v, want only the secret", got)
	}
	if got := (*Secrets)(nil).Names([]byte("x")); got != nil {
		t.Errorf("nil Secrets: %v", got)
	}
	if got := NewSecrets().Names([]byte("x")); got != nil {
		t.Errorf("empty Secrets: %v", got)
	}
}
