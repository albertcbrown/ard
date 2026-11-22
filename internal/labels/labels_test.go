package labels

import (
	"strings"
	"testing"
)

func TestConstructors(t *testing.T) {
	tests := []struct{ got, want string }{
		{Request(StageTriage, "lead"), Pending},
		{Request(StageTriage, "architect"), "ard:triage:architect"},
		{Running(StageTriage, "lead"), "ard:triage:lead:running"},
		{Done(StageTriage, "lead"), "ard:triage:lead:done"},
		{Running(StageTriage, "architect"), "ard:triage:architect:running"},
		{Done(StageTriage, "architect"), "ard:triage:architect:done"},
		{Request(StageWorker, "engineer"), "ard:worker:engineer"},
		{Running(StageWorker, "engineer"), "ard:worker:engineer:running"},
		{Done(StageWorker, "engineer"), "ard:worker:engineer:done"},
		{Request(StageReview, "eng-review"), "ard:review:eng-review"},
		{Running(StageReview, "eng-review"), "ard:review:eng-review:running"},
		{Done(StageReview, "eng-review"), "ard:review:eng-review:done"},
		{Changes("eng-review"), "ard:review:eng-review:changes"},
		{Scanner("cve"), "ard:scanner:cve"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Parsed
		ok   bool
	}{
		{Pending, Parsed{Kind: KindStatic, Name: Pending}, true},
		{ExecutorFailed, Parsed{Kind: KindStatic, Name: ExecutorFailed}, true},
		{TestsFailed, Parsed{Kind: KindStatic, Name: TestsFailed}, true},
		{"ard:scanner:cve", Parsed{Kind: KindScanner, Name: "ard:scanner:cve", Role: "cve"}, true},
		{"ard:triage:architect", Parsed{Kind: KindRole, Name: "ard:triage:architect", Stage: StageTriage, Role: "architect"}, true},
		{"ard:triage:lead:running", Parsed{Kind: KindRole, Name: "ard:triage:lead:running", Stage: StageTriage, Role: "lead", State: StateRunning}, true},
		{"ard:review:x:changes", Parsed{Kind: KindRole, Name: "ard:review:x:changes", Stage: StageReview, Role: "x", State: StateChanges}, true},
		{"ARD:Review:X:Done", Parsed{Kind: KindRole, Name: "ard:review:x:done", Stage: StageReview, Role: "x", State: StateDone}, true},
		{"ard:triage:x:changes", Parsed{}, false},
		{"ard:worker:x:changes", Parsed{}, false},
		{"ard:bogus:x", Parsed{}, false},
		{"ard:triage:lead", Parsed{}, false},
		{"ard:triage:x:weird", Parsed{}, false},
		{"ard:scanner:a:b", Parsed{}, false},
		{"ard:", Parsed{}, false},
		{"pending", Parsed{}, false},
		{"bug", Parsed{}, false},
	}
	for _, tt := range tests {
		got, ok := Parse(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseStage(t *testing.T) {
	for _, s := range []string{"triage", "worker", "review"} {
		if got, ok := ParseStage(s); !ok || string(got) != s {
			t.Errorf("ParseStage(%q) = %q, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "reviewer", "scanner", "Triage"} {
		if _, ok := ParseStage(s); ok {
			t.Errorf("ParseStage(%q) ok, want false", s)
		}
	}
}

func TestValidRoleName(t *testing.T) {
	for _, n := range []string{"a", "0", "eng-review", "a-b-c", "9x"} {
		if !ValidRoleName(n) {
			t.Errorf("ValidRoleName(%q) = false", n)
		}
	}
	for _, n := range []string{"", "-x", "Foo", "a:b", "a_b", "a b", "é"} {
		if ValidRoleName(n) {
			t.Errorf("ValidRoleName(%q) = true", n)
		}
	}
}

func TestStaticAreParsedStatic(t *testing.T) {
	if len(Static()) != 9 {
		t.Fatalf("Static() has %d labels, want 9", len(Static()))
	}
	for _, s := range Static() {
		if p, ok := Parse(s); !ok || p.Kind != KindStatic {
			t.Errorf("Parse(%q) = %+v, %v", s, p, ok)
		}
	}
}

func TestCheckIntake(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"", false},
		{"ard:pending", false},
		{"ARD:Pending", false},
		{"ard:intake", false},
		{"ard:ready-for-ard", false},
		{"ard:needs-human", true},
		{"ARD:NEEDS-HUMAN", true},
		{"ard:hold", true},
		{"ard:pause", true},
		{"ard:approved", true},
		{"ard:keep", true},
		{"ard:executor:failed", true},
		{"ard:triage:lead:done", true},
		{"ard:worker:ghost", true}, // no such role is needed: the grammar alone bans it
		{"ard:review:x:changes", true},
		{"ard:scanner:cve", true},
		// Parse rejects it: it is not a label ard applies, because the lead's
		// request is ard:pending.
		{"ard:triage:lead", false},
	}
	for _, tt := range tests {
		err := CheckIntake(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("CheckIntake(%q) = %v, wantErr %v", tt.name, err, tt.wantErr)
			continue
		}
		if err != nil && (!strings.Contains(err.Error(), "uses itself") || !strings.Contains(err.Error(), tt.name)) {
			t.Errorf("CheckIntake(%q) error = %q, want it to say 'uses itself' and name the label", tt.name, err)
		}
	}
}
