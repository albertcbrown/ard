package servecmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/state"
)

type failingMeta struct{}

func (failingMeta) Meta(context.Context, string) (string, error)  { return "", state.ErrNotFound }
func (failingMeta) SetMeta(context.Context, string, string) error { return errors.New("disk full") }

var testWindows = []agent.Window{{Type: "five_hour", Utilization: 0.3, Status: "allowed"}}

func TestReadingObserver(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth agent.Auth
		ws   []agent.Window
		want int
	}{
		{"oauth with windows is recorded", agent.AuthOAuthToken, testWindows, 1},
		{"api key is not recorded", agent.AuthAPIKey, testWindows, 0},
		{"no credential is not recorded", "", testWindows, 0},
		{"no windows is a no-op", agent.AuthOAuthToken, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := limitWiring(t, &fakeClock{limitT0})
			var buf bytes.Buffer
			w.Log = slog.New(slog.NewTextHandler(&buf, nil))
			w.lim = nil // reopen on first use, with the buffer logger
			w.Model.Auth = tc.auth
			readingObserver{w}.Observe(context.Background(), tc.ws)
			b, err := w.limitsBook(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := len(b.Readings()); got != tc.want {
				t.Errorf("readings = %d, want %d", got, tc.want)
			}
			if got := strings.Contains(buf.String(), "limits.reading"); got != (tc.want == 1) {
				t.Errorf("limits.reading logged = %v\n%s", got, buf.String())
			}
		})
	}
}

func TestReadingObserverErrorIsSwallowed(t *testing.T) {
	w := newTestWiring(t)
	var buf bytes.Buffer
	w.Log = slog.New(slog.NewTextHandler(&buf, nil))
	book, err := limits.Open(context.Background(), failingMeta{}, nil, w.Log)
	if err != nil {
		t.Fatal(err)
	}
	w.lim = book
	w.Model.Auth = agent.AuthOAuthToken
	readingObserver{w}.Observe(context.Background(), testWindows) // must not panic or return an error
	if !strings.Contains(buf.String(), "limits.observe_failed") {
		t.Errorf("no limits.observe_failed:\n%s", buf.String())
	}
}
