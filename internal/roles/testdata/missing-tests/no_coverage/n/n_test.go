package n

import "testing"

func TestN(t *testing.T) {
	if Named() != 1 {
		t.Fatal("bad")
	}
}
