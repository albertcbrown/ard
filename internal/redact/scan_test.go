package redact

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func ruleIDs(f []Finding) []string {
	var o []string
	for _, x := range f {
		o = append(o, x.Rule)
	}
	return o
}

func TestScannerPositivePerRule(t *testing.T) {
	for id, tok := range plantedTokens() {
		t.Run(id, func(t *testing.T) {
			in := "line one\nsome text " + tok + " more\n"
			f, err := Builtin().Scan(context.Background(), strings.NewReader(in))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, x := range f {
				if x.Rule == id {
					found = true
					if x.Line != 2 || x.Col < 1 {
						t.Errorf("finding = %+v", x)
					}
				}
			}
			if !found {
				t.Errorf("rule %s not found in %v", id, f)
			}
		})
	}
}

func TestScannerNegatives(t *testing.T) {
	for name, in := range map[string]string{
		"redaction marker": "TOKEN=[REDACTED:TOKEN]",
		"git sha":          "commit 3b18e512dba79e4c8300dd08aeb37f8e728b8dad",
		"ulid":             "id 01ARZ3NDEKTSV4RRFFQ69G5FAV done",
		"placeholder":      "password=changeme-changeme-changeme",
		"env ref":          "token: $" + "{ARD_TOKEN_VALUE_HERE}",
		"repeated":         "secret=aaaaaaaaaaaaaaaaaaaaaaaa",
		"low entropy":      "token=abababababababababababab",
		"session id":       `{"type":"system","session_id":"3f2a9c1e-7b44-4d0e-9a52-0c1d2e3f4a5b"}`,
		"plain prose":      "the quick brown fox jumps over the lazy dog",
		"example marker":   "api_key=your-example-api-key-goes-here",
	} {
		t.Run(name, func(t *testing.T) {
			f, err := Builtin().Scan(context.Background(), strings.NewReader(in))
			if err != nil || len(f) != 0 {
				t.Errorf("findings = %v err = %v", f, err)
			}
		})
	}
}

func TestScannerOverlongLineSeam(t *testing.T) {
	tok := plantedTokens()["github-token"]
	for _, off := range []int{scanWindow - len(tok)/2, scanWindow + 5, 2*scanWindow - 10} {
		line := make([]byte, 3*scanWindow)
		for i := range line {
			line[i] = 'a' + byte(i%7)
			if i%50 == 49 {
				line[i] = ' '
			}
		}
		line[off-1] = ' '
		copy(line[off:], tok)
		in := "first\n" + string(line) + "\nlast\n"
		f, err := Builtin().Scan(context.Background(), strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		if len(f) != 1 || f[0].Rule != "github-token" || f[0].Line != 2 || f[0].Col != off+1 {
			t.Errorf("off %d: findings = %+v", off, f)
		}
	}
}

func TestScannerHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in := strings.Repeat("hello\n", 5000)
	if _, err := Builtin().Scan(ctx, strings.NewReader(in)); err == nil {
		t.Error("want error from cancelled ctx")
	}
}

func TestEntropy(t *testing.T) {
	if e := entropy([]byte("aaaa")); e != 0 {
		t.Errorf("entropy = %v", e)
	}
	if e := entropy([]byte(randStr(50, 64))); e < 4 {
		t.Errorf("entropy = %v", e)
	}
	_ = fmt.Sprint
}
