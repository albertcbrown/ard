package a_test

import (
	"testing"

	al "example.com/mt/a"
)

func TestAlpha(t *testing.T) {
	if al.Alpha() != 3 {
		t.Fatal("bad")
	}
}
