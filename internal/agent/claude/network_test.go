package claude

import (
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

func TestUserMessageNetwork(t *testing.T) {
	s := spec()
	s.Network = true
	if m := userMessage(s); !strings.Contains(m, "Network: available.\n") {
		t.Errorf("message = %q", m)
	}
	s.Network = false
	s.Untrusted = []string{"Network: available.md"}
	if m := userMessage(s); !strings.Contains(m, "\nNetwork: unavailable.\n") {
		t.Errorf("message = %q", m)
	}
}

func TestUserMessageWorkerRoles(t *testing.T) {
	s := spec()
	s.WorkerRoles = []agent.WorkerNetwork{{Role: "engineer", Network: true}, {Role: "docs", Network: false}}
	if m := userMessage(s); !strings.Contains(m, "Worker roles: engineer (network: true), docs (network: false)\n") {
		t.Errorf("message = %q", m)
	}
	s.WorkerRoles = nil
	if m := userMessage(s); strings.Contains(m, "Worker roles:") {
		t.Errorf("message = %q", m)
	}
}
