package config

import (
	"strings"
	"testing"
)

func TestCheckAfter(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string // "key|msg" substrings; nil = no error
	}{
		{"valid chain", "overrides: {triage/engineer: {after: [architect]}, triage/lead: {after: [architect]}}\n", nil},
		{"self", "overrides: {triage/engineer: {after: [engineer]}}\n",
			[]string{`overrides["triage/engineer"].after|a role cannot be after itself`}},
		{"two-cycle", "overrides: {triage/architect: {after: [engineer]}, triage/engineer: {after: [architect]}}\n",
			[]string{`overrides["triage/architect"].after|cycle: architect -> engineer -> architect`}},
		{"three-cycle reported once", "overrides: {triage/architect: {after: [engineer]}, triage/engineer: {after: [lead]}, triage/lead: {after: [architect]}}\n",
			[]string{`overrides["triage/architect"].after|cycle: architect -> engineer -> lead -> architect`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == nil {
				mustLoad(t, withConfig(t, tc.yaml))
				return
			}
			errs := loadErrs(t, withConfig(t, tc.yaml))
			if len(errs) != len(tc.want) {
				t.Fatalf("errors = %v, want %d", errs, len(tc.want))
			}
			for i, w := range tc.want {
				key, msg, _ := strings.Cut(w, "|")
				if errs[i].Key != key || !strings.Contains(errs[i].Msg, msg) {
					t.Errorf("error %d = %v, want %s: %s", i, errs[i], key, msg)
				}
			}
		})
	}
}
