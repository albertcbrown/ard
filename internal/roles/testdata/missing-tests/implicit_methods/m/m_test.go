package m

import (
	"fmt"
	"testing"
)

func TestImplicit(t *testing.T) {
	if fmt.Sprint(E{}) != "e" || fmt.Sprint(S{}) != "s" {
		t.Fatal("bad")
	}
}
