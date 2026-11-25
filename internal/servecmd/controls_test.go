package servecmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPauseUnpauseFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pause", "o", "r")
	c := &controls{w: &wiring{}, pausePath: p}
	for i := 0; i < 2; i++ { // idempotent
		if err := c.Pause(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(p); err != nil {
		t.Fatal(err)
	}
	if err := c.Unpause(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("pause file remains: %v", err)
	}
	if err := c.Unpause(); err != nil {
		t.Errorf("unpause without file: %v", err)
	}
}

func TestUnpauseRefusesDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pause")
	if err := os.MkdirAll(filepath.Join(p, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := &controls{w: &wiring{}, pausePath: p}
	if err := c.Unpause(); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(p, "x")); err != nil {
		t.Error("directory was touched")
	}
}

func TestSlotKeyBeforePool(t *testing.T) {
	c := &controls{w: &wiring{}}
	if msg, err := c.step(1); err != nil || msg != "pool not ready" {
		t.Errorf("%q %v", msg, err)
	}
}
