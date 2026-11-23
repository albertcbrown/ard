package p

import "testing"

func TestP(t *testing.T) {
	var l List[int]
	l.Push(1)
	if Covered() != 2 {
		t.Fatal("bad")
	}
}
