package config

import (
	"strings"
	"testing"
)

func TestNetworkDefaultAndOverride(t *testing.T) {
	res := mustLoad(t, withConfig(t, "agent: kimi\noverrides:\n  scanners/cve: {network: false}\n  triage/lead: {network: false}\n  workers/engineer: {network: false}\n"))
	for _, r := range res.Roles {
		want := !(r.Kind == KindScanner && r.Name == "cve" || r.Kind == KindTriage && r.Name == "lead" || r.Kind == KindWorker && r.Name == "engineer")
		if r.Network != want {
			t.Errorf("%s/%s network = %v, want %v", r.Kind, r.Name, r.Network, want)
		}
	}
}

func TestNetworkRejectsNonBool(t *testing.T) {
	for _, v := range []string{`"maybe"`, "3"} {
		_, err := LoadFS(withConfig(t, "agent: kimi\noverrides:\n  scanners/cve: {network: "+v+"}\n"), "/repo")
		if err == nil || !strings.Contains(err.Error(), "network") {
			t.Errorf("network: %s: err = %v", v, err)
		}
	}
}
