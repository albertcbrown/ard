package agent_test

import (
	"errors"
	"io"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
)

type fake string

func (f fake) Name() string                             { return string(f) }
func (fake) Command(agent.Spec) (agent.Command, error)  { return agent.Command{}, nil }
func (fake) Parse(io.Reader, int) (agent.Result, error) { return agent.Result{}, nil }

func TestRegistry(t *testing.T) {
	if _, err := agent.NewRegistry(fake("a"), fake("a")); err == nil {
		t.Error("duplicate accepted")
	}
	r, err := agent.NewRegistry(fake("a"), fake("b"))
	if err != nil {
		t.Fatal(err)
	}
	if a, err := r.Get("b"); err != nil || a.Name() != "b" {
		t.Errorf("Get b = %v, %v", a, err)
	}
	if _, err := r.Get("kimi"); !errors.Is(err, agent.ErrUnknownAgent) {
		t.Errorf("err = %v", err)
	}
}

func TestTransient(t *testing.T) {
	for st, want := range map[int]bool{429: true, 500: true, 502: true, 503: true, 529: true, 400: false, 0: false} {
		if got := (agent.Result{APIErrorStatus: st}).Transient(); got != want {
			t.Errorf("%d: %v", st, got)
		}
	}
	if (agent.Result{OK: true, APIErrorStatus: 429}).Transient() {
		t.Error("OK result transient")
	}
}
