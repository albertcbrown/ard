package executor

import (
	"errors"
	"testing"
)

func TestEnvPolicy(t *testing.T) {
	p := EnvPolicy{
		AllowEnv:   map[string]bool{"ANTHROPIC_API_KEY": true},
		AllowFixed: map[string]bool{"CLAUDE_CONFIG_DIR": true, "MY_KEY": true},
	}
	tests := []struct {
		name  string
		fixed bool
		in    []string
		bad   bool
	}{
		{"two credentials", false, []string{"ANTHROPIC_API_KEY=a", "ANTHROPIC_API_KEY=b"}, true},
		{"GH_TOKEN", false, []string{"GH_TOKEN=x"}, true},
		{"ARD_X", false, []string{"ARD_X=1"}, true},
		{"not allowed", false, []string{"OTHER=1"}, true},
		{"no value", false, []string{"ANTHROPIC_API_KEY"}, true},
		{"allowed", false, []string{"ANTHROPIC_API_KEY=k"}, false},
		{"fixed secret-shaped", true, []string{"MY_KEY=x"}, true},
		{"fixed base name", true, []string{"HOME=/x"}, true},
		{"fixed not allowed", true, []string{"FOO=1"}, true},
		{"fixed allowed", true, []string{"CLAUDE_CONFIG_DIR=/cfg"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			check := p.CheckEnv
			if tc.fixed {
				check = p.CheckFixed
			}
			err := check(tc.in)
			if tc.bad != (err != nil) || (tc.bad && !errors.Is(err, ErrForbiddenEnv)) {
				t.Errorf("err = %v, want bad=%v", err, tc.bad)
			}
		})
	}
}

func TestArdEnv(t *testing.T) {
	got := ArdEnv(Handle{JobID: "J", Issue: 3, Network: NetworkOn})
	want := "ARD_NETWORK=1"
	found := false
	for _, e := range got {
		found = found || e == want
	}
	if !found {
		t.Errorf("%v lacks %s", got, want)
	}
}
