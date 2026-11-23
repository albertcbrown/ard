package redact

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGitleaks writes a shell script that stands in for gitleaks. body runs
// with $report set to the --report-path argument and $src to --source.
func fakeGitleaks(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script fake")
	}
	script := `#!/bin/sh
all="$*"; report=""; src=""
while [ $# -gt 0 ]; do
  case "$1" in
    --report-path) report="$2"; shift;;
    --source) src="$2"; shift;;
  esac
  shift
done
` + body + "\n"
	p := filepath.Join(t.TempDir(), "gitleaks")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func scanWith(t *testing.T, path, in string) ([]Finding, error) {
	t.Helper()
	return Gitleaks(GitleaksOptions{Path: path}).Scan(context.Background(), strings.NewReader(in))
}

func TestGitleaksHit(t *testing.T) {
	p := fakeGitleaks(t, `echo '[{"RuleID":"aws-access-token","StartLine":3,"StartColumn":7,"Match":"SECRETTEXT","Secret":"SECRETTEXT"}]' > "$report"; exit 2`)
	got, err := scanWith(t, p, "a\nb\nc\n")
	if err != nil {
		t.Fatal(err)
	}
	want := Finding{Rule: "gitleaks:aws-access-token", Line: 3, Col: 7}
	if len(got) != 1 || got[0] != want {
		t.Errorf("findings = %+v, want [%+v]", got, want)
	}
}

func TestGitleaksClean(t *testing.T) {
	p := fakeGitleaks(t, `echo '[]' > "$report"; exit 0`)
	got, err := scanWith(t, p, "hello\n")
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v; want clean", got, err)
	}
}

func TestGitleaksMissingBinary(t *testing.T) {
	got, err := scanWith(t, filepath.Join(t.TempDir(), "nope"), "x")
	if err == nil || got != nil {
		t.Errorf("got %+v, %v; want error", got, err)
	}
}

func TestGitleaksErrorExit(t *testing.T) {
	p := fakeGitleaks(t, `echo "LEAKED-STDERR-TEXT" >&2; exit 1`)
	got, err := scanWith(t, p, "x")
	if err == nil || got != nil {
		t.Fatalf("got %+v, %v; want error", got, err)
	}
	if strings.Contains(err.Error(), "LEAKED-STDERR-TEXT") {
		t.Errorf("error echoes stderr: %v", err)
	}
}

func TestGitleaksBadReport(t *testing.T) {
	for name, body := range map[string]string{
		"garbage with exit 0":    `echo 'not json' > "$report"; exit 0`,
		"empty report, exit 2":   `echo '[]' > "$report"; exit 2`,
		"no report, exit 0":      `exit 0`,
		"findings with exit 0":   `echo '[{"RuleID":"x","StartLine":1,"StartColumn":1}]' > "$report"; exit 0`,
		"garbage report, exit 2": `echo '{' > "$report"; exit 2`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := scanWith(t, fakeGitleaks(t, body), "x")
			if err == nil || got != nil {
				t.Errorf("got %+v, %v; want error", got, err)
			}
		})
	}
}

func TestGitleaksTimeoutAndCancel(t *testing.T) {
	p := fakeGitleaks(t, `sleep 5`)
	start := time.Now()
	_, err := Gitleaks(GitleaksOptions{Path: p, Timeout: 100 * time.Millisecond}).Scan(context.Background(), strings.NewReader("x"))
	if err == nil {
		t.Error("timeout: want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Gitleaks(GitleaksOptions{Path: p}).Scan(ctx, strings.NewReader("x")); err == nil {
		t.Error("cancelled: want error")
	}
	if time.Since(start) > 4*time.Second {
		t.Error("fake was not killed")
	}
}

// The scanner's own child keeps the output pipe open after the timeout kills the scanner (#198). Scan must still return
// within about the timeout plus gitleaksWaitDelay, not when the child exits. Without WaitDelay this takes the full 5 s on
// Linux (it failed TestGitleaksTimeoutAndCancel in a Debian CI pod, whose /bin/sh is dash); on macOS both pass either way.
func TestGitleaksTimeoutChildHoldsPipe(t *testing.T) {
	p := fakeGitleaks(t, `sleep 5 & wait`)
	start := time.Now()
	_, err := Gitleaks(GitleaksOptions{Path: p, Timeout: 100 * time.Millisecond}).Scan(context.Background(), strings.NewReader("x"))
	if err == nil {
		t.Error("timeout: want error")
	}
	if d := time.Since(start); d > gitleaksWaitDelay+2*time.Second {
		t.Errorf("Scan returned after %v, want about the timeout plus %v", d, gitleaksWaitDelay)
	}
}

func TestGitleaksArgs(t *testing.T) {
	out := t.TempDir()
	p := fakeGitleaks(t, `echo "$all" > `+out+`/argv; cp "$src" `+out+`/input; echo "$src" > `+out+`/srcpath; echo '[]' > "$report"`)
	if _, err := scanWith(t, p, "redacted bytes\n"); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(filepath.Join(out, "argv"))
	for _, w := range []string{"detect", "--no-git", "--report-format json"} {
		if !strings.Contains(string(argv), w) {
			t.Errorf("argv %q lacks %q", argv, w)
		}
	}
	in, _ := os.ReadFile(filepath.Join(out, "input"))
	if !bytes.Equal(in, []byte("redacted bytes\n")) {
		t.Errorf("input = %q", in)
	}
	src, _ := os.ReadFile(filepath.Join(out, "srcpath"))
	if _, err := os.Stat(filepath.Dir(strings.TrimSpace(string(src)))); !os.IsNotExist(err) {
		t.Errorf("temp dir left behind: %v", err)
	}
}

type fixedScanner struct {
	f   []Finding
	err error
}

func (s fixedScanner) Scan(context.Context, io.Reader) ([]Finding, error) { return s.f, s.err }

func TestMulti(t *testing.T) {
	hit := Finding{Rule: "builtin", Line: 1, Col: 1}
	got, err := Multi(fixedScanner{f: []Finding{hit}}, fixedScanner{}).Scan(context.Background(), strings.NewReader("x"))
	if err != nil || len(got) != 1 || got[0] != hit {
		t.Errorf("got %+v, %v", got, err)
	}
	for _, ss := range [][]Scanner{
		{fixedScanner{f: []Finding{hit}}, fixedScanner{err: os.ErrInvalid}},
		{fixedScanner{err: os.ErrInvalid}, fixedScanner{}},
	} {
		got, err := Multi(ss...).Scan(context.Background(), strings.NewReader("x"))
		if err == nil || got != nil {
			t.Errorf("got %+v, %v; want error", got, err)
		}
	}
}

func TestLogWithGitleaks(t *testing.T) {
	write := func(t *testing.T) (string, string) {
		d := t.TempDir()
		src := filepath.Join(d, "in.log")
		if err := os.WriteFile(src, []byte("plain log\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return src, filepath.Join(d, "out.log")
	}
	t.Run("hit", func(t *testing.T) {
		p := fakeGitleaks(t, `echo '[{"RuleID":"r","StartLine":1,"StartColumn":1}]' > "$report"; exit 2`)
		r, err := New(nil, Options{Scanner: Multi(Builtin(), Gitleaks(GitleaksOptions{Path: p}))})
		if err != nil {
			t.Fatal(err)
		}
		src, dst := write(t)
		res, err := r.Log(context.Background(), src, dst)
		if err != nil || !res.Flagged || res.Clean != nil {
			t.Errorf("res = %+v, err = %v; want flagged", res, err)
		}
	})
	t.Run("error", func(t *testing.T) {
		p := fakeGitleaks(t, `exit 1`)
		r, err := New(nil, Options{Scanner: Multi(Builtin(), Gitleaks(GitleaksOptions{Path: p}))})
		if err != nil {
			t.Fatal(err)
		}
		src, dst := write(t)
		if _, err := r.Log(context.Background(), src, dst); err == nil {
			t.Error("want error")
		}
		if ents, _ := os.ReadDir(filepath.Dir(dst)); len(ents) != 1 {
			t.Errorf("leftover files: %v", ents)
		}
	})
}
