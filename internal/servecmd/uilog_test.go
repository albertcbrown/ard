package servecmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/snapshot"
)

func TestUIWanted(t *testing.T) {
	env := func(term string) func(string) string { return func(string) string { return term } }
	var buf bytes.Buffer
	pr, pw, _ := os.Pipe()
	defer pr.Close()
	defer pw.Close()
	for _, c := range []struct {
		name    string
		mode    string
		out     io.Writer
		term    string
		want    bool
		wantErr bool
	}{
		{"buffer", uiAuto, &buf, "xterm", false, false},
		{"pipe", uiAuto, pw, "xterm", false, false},
		{"no-ui", uiOff, pw, "xterm", false, false},
		{"dumb", uiAuto, pw, "dumb", false, false},
		{"--ui on a pipe", uiOn, pw, "xterm", false, true},
		{"--ui non-file", uiOn, &buf, "xterm", false, true},
	} {
		got, err := uiWanted(c.mode, pr, c.out, env(c.term))
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: got %v, %v", c.name, got, err)
		}
	}
}

func TestServeLog(t *testing.T) {
	home := t.TempDir()
	lf, path, err := openServeLog(home, github.Repo{Owner: "o", Name: "r"})
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if want := filepath.Join(home, ".ard", "logs", "o", "r", "serve.log"); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", st.Mode())
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode())
	}
	lf.cap = 100
	for i := 0; i < 20; i++ {
		if _, err := lf.Write([]byte(strings.Repeat("x", 30) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{path, path + ".1"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size() > 100 || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: size %d mode %v", p, st.Size(), st.Mode())
		}
	}
}

type fakeProgram struct {
	quit chan struct{}
	once sync.Once
}

func (f *fakeProgram) Run() (tea.Model, error) { <-f.quit; return nil, nil }
func (f *fakeProgram) Send(m tea.Msg) {
	if _, ok := m.(tea.QuitMsg); ok {
		f.once.Do(func() { close(f.quit) })
	}
}

func TestRunWithUI(t *testing.T) {
	old := newProgram
	defer func() { newProgram = old }()
	var fp *fakeProgram
	newProgram = func(tea.Model, io.Reader, io.Writer) program {
		fp = &fakeProgram{quit: make(chan struct{})}
		return fp
	}
	take := func(context.Context) snapshot.Snapshot { return snapshot.Snapshot{} }

	// SIGINT path: ctx canceled, the controller stops gracefully.
	ctx, cancel := context.WithCancel(context.Background())
	stopped := false
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	err := runWithUI(ctx, func(c context.Context) error { <-c.Done(); stopped = true; return nil }, take, nil, nil, "o/r", "", nil, io.Discard)
	if err != nil || !stopped {
		t.Errorf("err %v stopped %v", err, stopped)
	}

	// UI quit path: the controller is canceled and waited for.
	stopped = false
	done := make(chan error, 1)
	go func() {
		done <- runWithUI(context.Background(), func(c context.Context) error { <-c.Done(); stopped = true; return nil }, take, nil, nil, "o/r", "", nil, io.Discard)
	}()
	time.Sleep(20 * time.Millisecond)
	fp.Send(tea.Quit())
	if err := <-done; err != nil || !stopped {
		t.Errorf("err %v stopped %v", err, stopped)
	}
}
