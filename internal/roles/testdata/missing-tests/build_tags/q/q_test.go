package q

import "testing"

func TestQ(t *testing.T) {
	if Q() != 1 {
		t.Fatal("bad")
	}
}
