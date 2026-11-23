package scanner

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeFindings(t *testing.T) {
	good := `{"fingerprint":"osv:GHSA-1:go.mod:x","title":"x: GHSA-1","severity":"high","group":"go.mod","files":["go.mod","a/b.go"],"body":"## md"}`
	tests := []struct {
		name    string
		in      string
		want    int
		wantErr string
	}{
		{"valid", good + "\n", 1, ""},
		{"no trailing newline", good, 1, ""},
		{"blank lines are fine", "\n" + good + "\n\n  \n", 1, ""},
		{"empty stream", "", 0, ""},
		{"minimal", `{"fingerprint":"a","title":"t","severity":"info"}`, 1, ""},
		{"unknown key", `{"fingerprint":"a","title":"t","severity":"low","extra":1}`, 0, "unknown key"},
		{"missing fingerprint", `{"title":"t","severity":"low"}`, 0, "fingerprint is required"},
		{"empty fingerprint", `{"fingerprint":"","title":"t","severity":"low"}`, 0, "fingerprint is required"},
		{"missing title", `{"fingerprint":"a","severity":"low"}`, 0, "title is required"},
		{"blank title", `{"fingerprint":"a","title":"  ","severity":"low"}`, 0, "title is required"},
		{"bad severity", `{"fingerprint":"a","title":"t","severity":"urgent"}`, 0, "severity"},
		{"missing severity", `{"fingerprint":"a","title":"t"}`, 0, "severity"},
		{"NUL in fingerprint", `{"fingerprint":"a\u0000b","title":"t","severity":"low"}`, 0, "fingerprint: invalid"},
		{"newline in fingerprint", `{"fingerprint":"a\nb","title":"t","severity":"low"}`, 0, "fingerprint: invalid"},
		{"newline in title", `{"fingerprint":"a","title":"t\nu","severity":"low"}`, 0, "one line"},
		{"long fingerprint", fmt.Sprintf(`{"fingerprint":%q,"title":"t","severity":"low"}`, strings.Repeat("a", 513)), 0, "longer than 512"},
		{"long title", fmt.Sprintf(`{"fingerprint":"a","title":%q,"severity":"low"}`, strings.Repeat("é", 201)), 0, "longer than 200"},
		{"dotdot file", `{"fingerprint":"a","title":"t","severity":"low","files":["a/../../b"]}`, 0, "file: not repo-relative"},
		{"leading dotdot file", `{"fingerprint":"a","title":"t","severity":"low","files":["../b"]}`, 0, "file: not repo-relative"},
		{"absolute file", `{"fingerprint":"a","title":"t","severity":"low","files":["/etc/passwd"]}`, 0, "repo-relative"},
		{"unclean file", `{"fingerprint":"a","title":"t","severity":"low","files":["./a"]}`, 0, "file: not repo-relative"},
		{"huge body", fmt.Sprintf(`{"fingerprint":"a","title":"t","severity":"low","body":%q}`, strings.Repeat("x", MaxBodyBytes+1)), 0, "body longer"},
		{"duplicate fingerprint", `{"fingerprint":"a","title":"t","severity":"low"}` + "\n" + `{"fingerprint":"a","title":"u","severity":"low"}`, 0, "duplicate fingerprint"},
		{"not json", `hello`, 0, "not a finding"},
		{"trailing data", `{"fingerprint":"a","title":"t","severity":"low"} {}`, 0, "trailing"},
		{"null", `null`, 0, "fingerprint is required"},
		{"wrong type", `{"fingerprint":1,"title":"t","severity":"low"}`, 0, "not a finding"},
		{"oversize line", fmt.Sprintf(`{"fingerprint":"a","title":"t","severity":"low","body":%q}`, strings.Repeat("x", MaxLineBytes)), 0, "longer than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeFindings(strings.NewReader(tt.in))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				if got != nil {
					t.Errorf("partial findings returned with an error: %v", got)
				}
				return
			}
			if err != nil || len(got) != tt.want {
				t.Fatalf("got %d findings, err %v; want %d", len(got), err, tt.want)
			}
		})
	}
}

func TestDecodeFindingsValues(t *testing.T) {
	in := `{"fingerprint":"osv:A:go.mod:x","title":"T","severity":"critical","group":"go.mod","files":["go.mod"],"body":"b"}`
	got, err := DecodeFindings(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := Finding{Fingerprint: "osv:A:go.mod:x", Title: "T", Severity: SevCritical, Group: "go.mod", Files: []string{"go.mod"}, Body: "b"}
	if fmt.Sprint(got[0]) != fmt.Sprint(want) {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestDecodeFindingsStreamAndCountLimits(t *testing.T) {
	line := func(i int) string {
		return fmt.Sprintf(`{"fingerprint":"fp-%d","title":"t","severity":"low"}`+"\n", i)
	}
	var b strings.Builder
	for i := 0; i < MaxFindings; i++ {
		b.WriteString(line(i))
	}
	if got, err := DecodeFindings(strings.NewReader(b.String())); err != nil || len(got) != MaxFindings {
		t.Fatalf("at the limit: %d findings, %v", len(got), err)
	}
	b.WriteString(line(MaxFindings))
	if _, err := DecodeFindings(strings.NewReader(b.String())); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("over the count limit: %v", err)
	}
	big := strings.Repeat(`{"fingerprint":"a","title":"t","severity":"low","body":"`+strings.Repeat("x", 60000)+`"}`+"\n", 300)
	if _, err := DecodeFindings(strings.NewReader(big)); err == nil {
		t.Error("oversize stream accepted")
	}
}

func TestDecodeErrorsReportsUpToTwenty(t *testing.T) {
	in := strings.Repeat("nope\n", 50)
	_, err := DecodeFindings(strings.NewReader(in))
	var de DecodeErrors
	if !errors.As(err, &de) || len(de) != 20 || de[0].Line != 1 || de[19].Line != 20 {
		t.Fatalf("err = %v", err)
	}
}

func TestSeverityRank(t *testing.T) {
	order := []Severity{SevInfo, SevLow, SevMedium, SevHigh, SevCritical}
	for i := 1; i < len(order); i++ {
		if order[i].Rank() <= order[i-1].Rank() {
			t.Errorf("%s does not outrank %s", order[i], order[i-1])
		}
	}
	if Severity("x").Valid() {
		t.Error("unknown severity is valid")
	}
}

func TestDecodeErrorsNeverEchoScannerText(t *testing.T) {
	const m = "sk-live-SECRET123"
	ok := func(fp string) string {
		return fmt.Sprintf(`{"fingerprint":%q,"title":"t","severity":"low"}`, fp)
	}
	withFile := func(f string) string {
		return fmt.Sprintf(`{"fingerprint":"a","title":"t","severity":"low","files":[%q]}`, f)
	}
	tests := []struct {
		name, in, want string
		line           int
	}{
		{"fingerprint control", `{"fingerprint":"` + m + `\u0000","title":"t","severity":"low"}`, "fingerprint:", 1},
		{"duplicate fingerprint", ok(m) + "\n" + ok(m), "duplicate fingerprint", 2},
		{"absolute file", withFile("/" + m), "file:", 1},
		{"dotdot file", withFile("../" + m), "file:", 1},
		{"unclean file", withFile("./" + m), "file:", 1},
		{"control file", `{"fingerprint":"a","title":"t","severity":"low","files":["` + m + `\u0001"]}`, "file:", 1},
		{"unknown key", `{"` + m + `":1,"fingerprint":"a","title":"t","severity":"low"}`, "unknown key", 1},
		{"bad severity", `{"fingerprint":"a","title":"t","severity":"` + m + `"}`, "severity:", 1},
		{"wrong type", `{"fingerprint":"a","title":1,"severity":"low","body":"` + m + `"}`, "wrong value type", 1},
		{"non-JSON", m, "invalid JSON", 1},
		{"truncated JSON", `{"fingerprint":"` + m, "invalid JSON", 1},
		{"trailing data", ok("a") + ` ` + m, "trailing", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeFindings(strings.NewReader(tt.in))
			var de DecodeErrors
			if !errors.As(err, &de) || len(de) == 0 {
				t.Fatalf("err = %v, want DecodeErrors", err)
			}
			if strings.Contains(err.Error(), m) {
				t.Errorf("error echoes scanner text: %v", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want containing %q", err, tt.want)
			}
			if de[len(de)-1].Line != tt.line {
				t.Errorf("line = %d, want %d", de[len(de)-1].Line, tt.line)
			}
		})
	}
	t.Run("multiple bad lines", func(t *testing.T) {
		in := m + "\n" + ok(m) + "\n" + withFile("/"+m) + "\n" + ok(m)
		_, err := DecodeFindings(strings.NewReader(in))
		var de DecodeErrors
		if !errors.As(err, &de) || len(de) != 3 {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(err.Error(), m) {
			t.Errorf("error echoes scanner text: %v", err)
		}
		for i, line := range []int{1, 3, 4} {
			if de[i].Line != line {
				t.Errorf("error %d on line %d, want %d", i, de[i].Line, line)
			}
		}
	})
}
