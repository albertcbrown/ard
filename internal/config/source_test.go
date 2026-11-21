package config

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func TestSourcePaths(t *testing.T) {
	want := []string{".ard/config.yaml", ".ard/scanners", ".ard/triage", ".ard/workers", ".ard/reviewers"}
	if got := SourcePaths(); !reflect.DeepEqual(got, want) {
		t.Errorf("SourcePaths() = %v, want %v", got, want)
	}
}

func TestLocalPaused(t *testing.T) {
	cfg := func(s string) fstest.MapFS { return fstest.MapFS{".ard/config.yaml": {Data: []byte(s)}} }
	tests := []struct {
		name    string
		fs      fstest.MapFS
		want    bool
		wantErr bool
	}{
		{"missing file", fstest.MapFS{}, false, false},
		{"paused", cfg("paused: true\n"), true, false},
		{"not paused", cfg("paused: false\nmax_pods: 2\n"), false, false},
		{"key absent", cfg("max_pods: 2\n"), false, false},
		{"other keys invalid are ignored", cfg("paused: true\nexecutor: 7\nmax_pods: [x]\n"), true, false},
		{"invalid yaml does not pause", cfg("paused: true\n  : : bad"), false, true},
		{"non-bool does not pause", cfg("paused: maybe\n"), false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LocalPaused(tc.fs)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("LocalPaused = %v, %v; want %v, err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
