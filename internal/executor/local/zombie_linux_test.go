//go:build linux

package local

import "testing"

func TestZombieStat(t *testing.T) {
	tests := []struct {
		name, stat string
		want       bool
	}{
		{"running", "4242 (sleep) S 1 4242 4242 0 -1", false},
		{"runnable", "4242 (go) R 1 4242", false},
		{"zombie", "4242 (sh) Z 1 4242 4242", true},
		{"dead", "4242 (sh) X 1", true},
		{"name with spaces and parens", "4242 (a ) Z b) S 1 4242", false},
		{"name ending in paren then zombie", "4242 (x) Z) Z 1", true},
		{"empty", "", false},
		{"no paren", "4242 sh Z 1", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := zombieStat(tc.stat); got != tc.want {
				t.Errorf("zombieStat(%q) = %v, want %v", tc.stat, got, tc.want)
			}
		})
	}
}
