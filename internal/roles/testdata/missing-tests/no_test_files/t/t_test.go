package t

import "testing"

func TestT(t *testing.T) {
	if T() != 1 {
		t.Fatal("bad")
	}
}
