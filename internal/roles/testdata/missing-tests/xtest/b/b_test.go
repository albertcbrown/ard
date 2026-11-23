package b

import (
	"testing"

	"example.com/mt/a"
)

func TestB(t *testing.T) {
	if B() != a.Beta()-1 {
		t.Fatal("bad")
	}
}
