package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkersLeft(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bad.go", "a\n<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> main\n")
	write("ok.go", "a\n<<< not a marker\n======\n")
	write("other.go", "<<<<<<< HEAD\n") // not in the recorded set
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	got := markersLeft(root, map[string]bool{"bad.go": true, "ok.go": true, "gone.go": true})
	if len(got) != 1 || got[0] != "bad.go" {
		t.Fatalf("markersLeft = %v, want [bad.go]", got)
	}
}

func TestQuoteName(t *testing.T) {
	if got := quoteName("a\nb\"c"); got != `"a\nb\"c"` {
		t.Errorf("quoteName = %s", got)
	}
	if got := quoteName(strings.Repeat("x", 1000)); len(got) > conflictNameCap+10 {
		t.Errorf("name not capped: %d bytes", len(got))
	}
}
