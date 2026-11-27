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

func TestScannerGenericAssignmentFalsePositives(t *testing.T) {
	for _, in := range []string{
		"Tokens: u.Tokens",
		"Token: w.Token",
		"Token: w.WorkerToken.Value",
		"TotalTokens: usage.TotalTokens,",
		`"cache_read_input_tokens": 1106888`,
		`"tokens": 11068881106888123`,
		"EnvAPI" + "Key = \"ANTHROPIC_" + "API_KEY\"",
		"token: CLAUDE_CODE_" + "OAUTH_TOKEN",
		"SECRET_NAME = \"GITHUB_APP_" + "PRIVATE_KEY\"",
	} {
		if f := scanString(t, in); len(f) != 0 {
			t.Errorf("%q: unexpected findings %v", in, f)
		}
	}
}

func TestScannerGenericAssignmentTruePositives(t *testing.T) {
	for _, in := range []string{
		"api_" + "key = " + randStr(13, 32),
		"token: \"" + randStr(14, 32) + "\"",
		"password = \"" + randStr(15, 24) + "." + randStr(16, 24) + "\"",
		"secret=" + randStr(17, 40) + "." + randStr(18, 40),
	} {
		found := false
		for _, f := range scanString(t, in) {
			if f.Rule == "generic-assignment" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: not flagged", in)
		}
	}
}

func scanString(t *testing.T, in string) []Finding {
	t.Helper()
	f, err := Builtin().Scan(context.Background(), strings.NewReader(in+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestScannerAWSExample(t *testing.T) {
	suffix := "EX" + "AMPLE"
	exID := "AK" + "IA" + "IOSFODNN7" + suffix
	exASIA := "AS" + "IA" + "ABCDEFGHI" + suffix
	exSecret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCY" + "EXAMPLE" + "KEY"
	real := "AK" + "IA" + "QZ7K2M9TXW4HPN6B"
	longEx := "AK" + "IA" + "ABCDEFGHIJKLM" + suffix
	cases := []struct {
		name string
		in   string
		want int
		col  int
	}{
		{"example id", exID, 0, 0},
		{"asia example", exASIA, 0, 0},
		{"example secret", "aws_secret_access_key = " + exSecret, 0, 0},
		{"real id", "key " + real, 1, 5},
		{"example then real", exID + " " + real, 1, 22},
		{"real then example", real + " " + exID, 1, 1},
		{"example plus X", exID + "X", 0, 0},
		{"too long example plus real", longEx + " " + real, 1, 0},
		{"lowercase suffix", "AK" + "IA" + "ABCDEFGHI" + "example", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := Builtin().Scan(context.Background(), strings.NewReader(c.in+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			if c.name == "too long example plus real" {
				// the 23-char token is not a valid id; only the real one counts
				if len(f) != 1 {
					t.Fatalf("findings = %v", f)
				}
				return
			}
			if len(f) != c.want {
				t.Fatalf("findings = %v, want %d", f, c.want)
			}
			if c.want == 1 && (f[0].Rule != "aws-access-key" || f[0].Col != c.col) {
				t.Errorf("finding = %+v, want col %d", f[0], c.col)
			}
		})
	}
}

// upperSnake returns a high-entropy upper-case string of n bytes with an
// underscore after every seventh byte, starting with a letter.
func upperSnake(seed uint64, n int) string {
	b := []byte("K" + strings.ToUpper(randStr(seed, n-1)))
	for i := 7; i < n-1; i += 8 {
		b[i] = '_'
	}
	return string(b)
}

func TestScannerGenericAssignmentEnvNameBoundaries(t *testing.T) {
	long := upperSnake(31, 64)
	tooLong := upperSnake(32, 65)
	noUnderscore := strings.ReplaceAll(upperSnake(33, 32), "_", "A")
	mixed := upperSnake(34, 40)
	mixed = strings.ToLower(mixed[:10]) + mixed[10:]
	lower := strings.ToLower(upperSnake(35, 40))
	snake := upperSnake(36, 40)
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{"short", upperSnake(30, 20), false},
		{"64 chars", long, false},
		{"65 chars", tooLong, true},
		{"no underscore", noUnderscore, true},
		{"mixed case", mixed, true},
		{"lower case", lower, true},
		{"dot segment", snake + "." + upperSnake(37, 20), true},
		{"plus", snake + "+" + upperSnake(38, 8), true},
		{"slash", snake + "/" + upperSnake(39, 8), true},
		{"equals", snake + "=", true},
		{"leading underscore", "_" + snake, true},
		{"leading digit", "7" + snake, true},
		{"double underscore", snake[:20] + "__" + snake[20:], true},
	} {
		for _, in := range []string{
			"api_" + "key = \"" + tc.value + "\"",
			"token: " + tc.value,
		} {
			found := false
			for _, f := range scanString(t, in) {
				if f.Rule == "generic-assignment" {
					found = true
				}
			}
			if found != tc.want {
				t.Errorf("%s: %q: flagged=%v, want %v", tc.name, in, found, tc.want)
			}
		}
	}
}
