package effects

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
)

const secretTok = "tok-for-test-only"

func varLive(t *testing.T, h http.HandlerFunc, tok func(context.Context) (string, error)) (*Live, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	var logs bytes.Buffer
	l := NewLive(nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	return l.WithOperator(tok, github.Config{BaseURL: srv.URL}), &logs
}

func tokOK(context.Context) (string, error) { return secretTok, nil }

func TestSetRepoVariableCreateOn404(t *testing.T) {
	var seen []string
	l, logs := varLive(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+secretTok {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodPatch {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}, tokOK)
	if err := l.SetRepoVariable(context.Background(), "o/r", "ARD_BOT_LOGIN", "x[bot]"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "PATCH /repos/o/r/actions/variables/ARD_BOT_LOGIN" || seen[1] != "POST /repos/o/r/actions/variables" {
		t.Errorf("requests %v", seen)
	}
	if strings.Contains(logs.String(), secretTok) {
		t.Error("token in logs")
	}
}

func TestSetRepoVariableUpdateOn200(t *testing.T) {
	n := 0
	l, _ := varLive(t, func(w http.ResponseWriter, r *http.Request) { n++; w.WriteHeader(http.StatusNoContent) }, tokOK)
	if err := l.SetRepoVariable(context.Background(), "o/r", "ARD_BOT_LOGIN", "x[bot]"); err != nil || n != 1 {
		t.Errorf("err %v requests %d", err, n)
	}
}

func TestSetRepoVariableRejectsBadInput(t *testing.T) {
	l, _ := varLive(t, func(http.ResponseWriter, *http.Request) { t.Error("request sent") }, tokOK)
	if err := l.SetRepoVariable(context.Background(), "bad", "ARD_X", "v"); err == nil {
		t.Error("bad repo accepted")
	}
	if err := l.SetRepoVariable(context.Background(), "o/r", "lower", "v"); err == nil {
		t.Error("bad name accepted")
	}
}

func TestSetRepoVariableNoToken(t *testing.T) {
	for _, tok := range []func(context.Context) (string, error){nil, func(context.Context) (string, error) { return "", errors.New("boom") }} {
		l, _ := varLive(t, func(http.ResponseWriter, *http.Request) { t.Error("request sent") }, tok)
		if err := l.SetRepoVariable(context.Background(), "o/r", "ARD_X", "v"); !errors.Is(err, ErrNoOperatorToken) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestDryRunSetRepoVariable(t *testing.T) {
	path := t.TempDir() + "/ledger.jsonl"
	d, err := NewDryRun(DryRunOptions{LedgerPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetRepoVariable(context.Background(), "o/r", "ARD_X", "v"); err != nil {
		t.Fatal(err)
	}
	b := mustRead(t, path)
	if !strings.Contains(b, `"SetRepoVariable"`) {
		t.Errorf("ledger = %s", b)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
