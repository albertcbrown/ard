package redact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newR(t *testing.T, s *Secrets, o Options) *Redactor {
	t.Helper()
	r, err := New(s, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func noTemps(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".redact-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestNewOptions(t *testing.T) {
	if _, err := New(nil, Options{MaxBytes: 1000, HeadBytes: 900}); !errors.Is(err, ErrBadOptions) {
		t.Errorf("err = %v", err)
	}
	if _, err := New(nil, Options{MaxFindings: -1}); !errors.Is(err, ErrBadOptions) {
		t.Errorf("negative MaxFindings err = %v", err)
	}
	if _, err := New(nil, Options{}); err != nil {
		t.Errorf("defaults: %v", err)
	}
}

func TestLogKnownValuesNeverReachClean(t *testing.T) {
	model := "sk-" + "ant-" + randStr(60, 40) // also matches a scanner rule
	other := randStr(61, 24)
	s := NewSecrets()
	_ = s.Add("MODEL_KEY", model)
	_ = s.Add("OTHER", other)
	dir := t.TempDir()
	in := fmt.Sprintf("start\nkey=%s\nnested {\"k\":%q}\nother %s end\n", model, model, other)
	src := writeFile(t, dir, "raw.log", in)
	dst := filepath.Join(dir, "out.log")
	r := newR(t, s, Options{})
	res, err := r.Log(context.Background(), src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Flagged || res.Clean == nil {
		t.Fatalf("flagged = %v findings = %v", res.Flagged, res.Findings)
	}
	full, _ := os.ReadFile(dst)
	for _, b := range [][]byte{res.Clean, full} {
		if bytes.Contains(b, []byte(model)) || bytes.Contains(b, []byte(other)) {
			t.Errorf("secret leaked: %q", b)
		}
		if !bytes.Contains(b, []byte("[REDACTED:MODEL_KEY]")) || !bytes.Contains(b, []byte("[REDACTED:OTHER]")) {
			t.Errorf("markers missing: %q", b)
		}
	}
	if res.Replaced["MODEL_KEY"] != 2 || res.Replaced["OTHER"] != 1 || res.OrigBytes != int64(len(in)) || res.RedactedPath != dst {
		t.Errorf("result = %+v", res)
	}
	if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode())
	}
	noTemps(t, dir)
}

func TestLogFlagsPatternHits(t *testing.T) {
	for id, tok := range plantedTokens() {
		t.Run(id, func(t *testing.T) {
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(old)
			dir := t.TempDir()
			src := writeFile(t, dir, "raw.log", "ok\nleaked "+tok+"\nok\n")
			res, err := newR(t, nil, Options{}).Log(context.Background(), src, filepath.Join(dir, "o.log"))
			if err != nil {
				t.Fatal(err)
			}
			if !res.Flagged || res.Clean != nil || res.FindingCount == 0 {
				t.Fatalf("res = %+v", res)
			}
			if !strings.Contains(strings.Join(ruleIDs(res.Findings), ","), id) {
				t.Errorf("rules = %v", ruleIDs(res.Findings))
			}
			for _, out := range []string{fmt.Sprintf("%+v", res), logs.String()} {
				if strings.Contains(out, tok) {
					t.Errorf("planted value leaked into %q", out)
				}
			}
			if _, err := os.Stat(res.RedactedPath); err != nil {
				t.Errorf("flagged log must stay host-local: %v", err)
			}
		})
	}
}

func TestLogHitInDroppedMiddleStillFlags(t *testing.T) {
	tok := plantedTokens()["github-token"]
	var sb strings.Builder
	sb.WriteString("head line\n")
	for sb.Len() < 3000 {
		sb.WriteString("filler filler filler\n")
	}
	sb.WriteString("middle " + tok + "\n")
	for sb.Len() < 20000 {
		sb.WriteString("filler filler filler\n")
	}
	dir := t.TempDir()
	src := writeFile(t, dir, "raw.log", sb.String())
	res, err := newR(t, nil, Options{MaxBytes: 4096, HeadBytes: 256}).Log(context.Background(), src, filepath.Join(dir, "o.log"))
	if err != nil || !res.Flagged || res.Clean != nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLogCapsAndKeepsKnownSecretsRedacted(t *testing.T) {
	secret := randStr(70, 20)
	s := NewSecrets()
	_ = s.Add("S", secret)
	var sb strings.Builder
	sb.WriteString("first " + secret + "\n")
	for sb.Len() < 3<<20 {
		sb.WriteString("a plain filler line of text\n")
	}
	sb.WriteString("last " + secret + "\n")
	dir := t.TempDir()
	src := writeFile(t, dir, "raw.log", sb.String())
	res, err := newR(t, s, Options{}).Log(context.Background(), src, filepath.Join(dir, "o.log"))
	if err != nil || res.Flagged {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if !res.Truncated || int64(len(res.Clean)) > DefaultMaxBytes || int64(len(res.Clean)) < DefaultMaxBytes-2048 {
		t.Errorf("truncated = %v len = %d", res.Truncated, len(res.Clean))
	}
	if !bytes.HasPrefix(res.Clean, []byte("first [REDACTED:S]\n")) || !bytes.HasSuffix(res.Clean, []byte("last [REDACTED:S]\n")) {
		t.Error("head or tail wrong")
	}
	if bytes.Contains(res.Clean, []byte(secret)) {
		t.Error("secret leaked")
	}
	i := bytes.Index(res.Clean, []byte("[ard: log truncated:"))
	if i < 0 || i > int(DefaultHeadBytes) {
		t.Errorf("marker at %d", i)
	}
}

func TestLogBinaryAndCRLF(t *testing.T) {
	in := "a\x00b\xff\xfe\r\nline two\r\n"
	dir := t.TempDir()
	src := writeFile(t, dir, "raw.log", in)
	res, err := newR(t, nil, Options{}).Log(context.Background(), src, filepath.Join(dir, "o.log"))
	if err != nil || res.Flagged || string(res.Clean) != in {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLogChunkSizes(t *testing.T) {
	secret := randStr(71, 16)
	s := NewSecrets()
	_ = s.Add("S", secret)
	in := strings.Repeat("xx ", 30) + secret + strings.Repeat(" yy", 30) + secret
	for chunk := 1; chunk <= 7; chunk++ {
		dir := t.TempDir()
		src := writeFile(t, dir, "raw.log", in)
		r := newR(t, s, Options{})
		r.chunk = chunk
		res, err := r.Log(context.Background(), src, filepath.Join(dir, "o.log"))
		if err != nil || string(res.Clean) != strings.ReplaceAll(in, secret, "[REDACTED:S]") {
			t.Fatalf("chunk %d: %q err %v", chunk, res.Clean, err)
		}
	}
}

type fakeScanner struct {
	err   error
	hang  bool
	found []Finding
}

func (f fakeScanner) Scan(ctx context.Context, r io.Reader) ([]Finding, error) {
	if f.hang {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.found, f.err
}

func TestLogFailsClosed(t *testing.T) {
	t.Run("scanner error", func(t *testing.T) {
		dir := t.TempDir()
		src := writeFile(t, dir, "raw.log", "hello\n")
		dst := filepath.Join(dir, "o.log")
		res, err := newR(t, nil, Options{Scanner: fakeScanner{err: errors.New("boom")}}).Log(context.Background(), src, dst)
		if err == nil || res.Clean != nil {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if _, e := os.Stat(dst); e == nil {
			t.Error("dst left behind")
		}
		noTemps(t, dir)
	})
	t.Run("scanner hangs, ctx cancelled", func(t *testing.T) {
		dir := t.TempDir()
		src := writeFile(t, dir, "raw.log", "hello\n")
		ctx, cancel := context.WithCancel(context.Background())
		go cancel()
		res, err := newR(t, nil, Options{Scanner: fakeScanner{hang: true}}).Log(ctx, src, filepath.Join(dir, "o.log"))
		if err == nil || res.Clean != nil {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		noTemps(t, dir)
	})
	t.Run("src missing", func(t *testing.T) {
		dir := t.TempDir()
		res, err := newR(t, nil, Options{}).Log(context.Background(), filepath.Join(dir, "nope"), filepath.Join(dir, "o.log"))
		if err == nil || res.Clean != nil {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("dst dir read-only", func(t *testing.T) {
		dir := t.TempDir()
		src := writeFile(t, dir, "raw.log", "hello\n")
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(ro, 0o700)
		if f, err := os.Create(filepath.Join(ro, "probe")); err == nil {
			f.Close()
			t.Skip("directory permissions not enforced")
		}
		res, err := newR(t, nil, Options{}).Log(context.Background(), src, filepath.Join(ro, "o.log"))
		if err == nil || res.Clean != nil {
			t.Fatalf("res = %+v err = %v", res, err)
		}
	})
	t.Run("ctx cancelled mid-stream", func(t *testing.T) {
		dir := t.TempDir()
		src := writeFile(t, dir, "raw.log", strings.Repeat("some line\n", 1000))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := newR(t, nil, Options{})
		r.chunk = 1
		r.open = func(p string) (io.ReadCloser, error) {
			f, err := os.Open(p)
			return &cancelAfterFirst{f, cancel}, err
		}
		res, err := r.Log(ctx, src, filepath.Join(dir, "o.log"))
		if err == nil || res.Clean != nil {
			t.Fatalf("res = %+v err = %v", res, err)
		}
		if _, e := os.Stat(filepath.Join(dir, "o.log")); e == nil {
			t.Error("dst left behind")
		}
		noTemps(t, dir)
	})
}

func TestText(t *testing.T) {
	secret := randStr(80, 20)
	s := NewSecrets()
	_ = s.Add("S", secret)
	r := newR(t, s, Options{})
	res, err := r.Text(context.Background(), []byte("summary "+secret))
	if err != nil || res.Flagged || string(res.Clean) != "summary [REDACTED:S]" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	res, err = r.Text(context.Background(), []byte("x "+plantedTokens()["jwt"]))
	if err != nil || !res.Flagged || res.Clean != nil {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestLogStreamsInBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("memory test skipped in -short mode")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "big.log")
	f, err := os.Create(src)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte("the quick brown fox jumps over the lazy dog again and again\n")
	block := bytes.Repeat(line, 1000)
	for n := 0; n < 64<<20; n += len(block) {
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	s := NewSecrets()
	_ = s.Add("S", randStr(90, 24))
	r := newR(t, s, Options{})
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, err := r.Log(context.Background(), src, filepath.Join(dir, "o.log"))
	runtime.ReadMemStats(&after)
	if err != nil || res.Flagged || !res.Truncated {
		t.Fatalf("res flagged=%v trunc=%v err=%v", res.Flagged, res.Truncated, err)
	}
	if d := after.TotalAlloc - before.TotalAlloc; d > 16<<20 {
		t.Errorf("allocated %d MiB for a 64 MiB log", d>>20)
	}
}

// cancelAfterFirst cancels the context once the first chunk has been read.
type cancelAfterFirst struct {
	*os.File
	cancel context.CancelFunc
}

func (c *cancelAfterFirst) Read(p []byte) (int, error) {
	n, err := c.File.Read(p)
	c.cancel()
	return n, err
}
