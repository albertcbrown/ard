package claude

import (
	"strings"
	"testing"
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
