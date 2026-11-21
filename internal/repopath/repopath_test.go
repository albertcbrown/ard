package repopath

import (
	"errors"
	"testing"
)

func TestParts(t *testing.T) {
	valid := []struct{ in, owner, name string }{
		{"a-b/c", "a-b", "c"},
		{"a/b-c", "a", "b-c"},
		{"Foo/Bar", "foo", "bar"},
		{"o/r.go", "o", "r.go"},
		{"o_x/r", "o_x", "r"},
		{"o/.github", "o", ".github"},
	}
	for _, tc := range valid {
		o, n, err := Parts(tc.in)
		if err != nil || o != tc.owner || n != tc.name {
			t.Errorf("Parts(%q) = %q, %q, %v; want %q, %q", tc.in, o, n, err, tc.owner, tc.name)
		}
	}
	for _, in := range []string{"", "a", "a/", "/b", "a/b/c", "./b", "../b", "a/.", "a/..",
		`a\b/c`, `a/b\c`, "a b/c", "a/b\x00", "é/x", "a:b/c"} {
		if _, _, err := Parts(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parts(%q) err = %v; want ErrInvalid", in, err)
		}
	}
}

func TestJoin(t *testing.T) {
	a, _ := Join("/b", "a-b/c")
	b, _ := Join("/b", "a/b-c")
	if a == b {
		t.Errorf("a-b/c and a/b-c collide at %s", a)
	}
	if a != "/b/a-b/c" {
		t.Errorf("Join = %q", a)
	}
	x, _ := Join("/b", "Foo/Bar")
	y, _ := Join("/b", "foo/bar")
	if x != y {
		t.Errorf("case differs: %q vs %q", x, y)
	}
	if _, err := Join("/b", "../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v", err)
	}
}
