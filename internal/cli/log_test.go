package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{"empty defaults to info", "", false},
		{"info", "info", false},
		{"debug", "debug", false},
		{"warn", "warn", false},
		{"error", "error", false},
		{"case-insensitive", "DeBuG", false},
		{"unknown", "chatty", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log, err := NewLogger(tt.level, &buf)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewLogger(%q) err = %v, wantErr %v", tt.level, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			log.Error("hello")
			if !strings.Contains(buf.String(), "hello") {
				t.Errorf("buffer %q missing %q", buf.String(), "hello")
			}
		})
	}
}

func TestNewLoggerFilters(t *testing.T) {
	var buf bytes.Buffer
	log, err := NewLogger("warn", &buf)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	log.Debug("dbg-token")
	log.Info("inf-token")
	log.Warn("wrn-token")
	log.Error("err-token")

	out := buf.String()
	for _, want := range []string{"wrn-token", "err-token"} {
		if !strings.Contains(out, want) {
			t.Errorf("buffer %q missing %q", out, want)
		}
	}
	for _, unwanted := range []string{"dbg-token", "inf-token"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("buffer %q should not contain %q", out, unwanted)
		}
	}
}
