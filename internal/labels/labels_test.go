package labels

import "testing"

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
