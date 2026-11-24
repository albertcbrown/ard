package config

import (
	"strings"
	"testing"
)

func TestTokenCount(t *testing.T) {
	ok := map[string]int64{"40M": 40_000_000, "1.5M": 1_500_000, "500k": 500_000, "500K": 500_000, "1234": 1234, "2.25k": 2250}
	for in, want := range ok {
		if got, err := ParseTokenCount(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-5", "abc", "5g", "1.5", "1e6", "0.0001k", "9999999999999999999M", "9999999999999M"} {
		_, err := ParseTokenCount(in)
		if err == nil || !strings.Contains(err.Error(), "token count") {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

func TestTokensPerDayConfig(t *testing.T) {
	res := mustLoad(t, withConfig(t, "tokens_per_day: 40M\noverrides:\n  workers/engineer: {tokens_per_day: 500k}\n"))
	eng, _ := res.Role(KindWorker, "engineer")
	lead, _ := res.Role(KindTriage, "lead")
	if c, set := res.RoleTokensPerDay(eng); !set || c != 500_000 {
		t.Errorf("engineer = %d, %v", c, set)
	}
	if c, set := res.RoleTokensPerDay(lead); !set || c != 40_000_000 {
		t.Errorf("lead = %d, %v", c, set)
	}
	plain := mustLoad(t, withConfig(t, "agent: claude\n"))
	peng, _ := plain.Role(KindWorker, "engineer")
	if c, set := plain.RoleTokensPerDay(peng); set || c != 0 {
		t.Errorf("unset = %d, %v", c, set)
	}
	if plain.render().TokensPerDay != 0 {
		t.Error("unset key rendered")
	}
	for _, bad := range []string{"0", "-3", "abc"} {
		errs := loadErrs(t, withConfig(t, "overrides:\n  workers/engineer: {tokens_per_day: "+bad+"}\n"))
		if !strings.Contains(errs.Error(), "tokens_per_day") {
			t.Errorf("%s: errs = %v", bad, errs)
		}
	}
}
