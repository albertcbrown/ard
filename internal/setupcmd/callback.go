package setupcmd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"time"
)

// DefaultCallbackTimeout bounds how long ard waits for the browser step.
const DefaultCallbackTimeout = 5 * time.Minute

// newState returns a one-time random state value.
func newState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("setup: random state: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// formPage is the auto-submitting page that POSTs the manifest to GitHub.
func formPage(manifestJSON []byte, postURL string) string {
	return `<!doctype html><html><body><p>Redirecting to GitHub…</p>
<form id="f" method="post" action="` + html.EscapeString(postURL) + `">
<input type="hidden" name="manifest" value="` + html.EscapeString(string(manifestJSON)) + `">
<noscript><button type="submit">Continue to GitHub</button></noscript>
</form><script>document.getElementById("f").submit();</script></body></html>`
}

// serveCallback listens on 127.0.0.1 (random port), serves the manifest form
// at "/" and waits for exactly one valid GET /callback carrying state. ready
// is called with the local URL once the listener is up. The server is shut
// down on return: after the code, on timeout, or on context cancel.
func serveCallback(ctx context.Context, render func(base string) (manifestJSON []byte, postURL string), state string, timeout time.Duration, ready func(localURL string)) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("setup: listen on loopback: %w", err)
	}
	manifestJSON, postURL := render("http://" + ln.Addr().String())
	page := formPage(manifestJSON, postURL)
	codeCh := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		got := q.Get("state")
		if subtle.ConstantTimeCompare([]byte(got), []byte(state)) != 1 || q.Get("code") == "" {
			http.Error(w, "invalid state or code", http.StatusBadRequest)
			return
		}
		select {
		case codeCh <- q.Get("code"):
			_, _ = w.Write([]byte("ard: GitHub App created. You can close this tab."))
		default:
			http.Error(w, "already used", http.StatusGone)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			_ = srv.Close()
		}
	}()
	if ready != nil {
		ready("http://" + ln.Addr().String() + "/")
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case code := <-codeCh:
		return code, nil
	case <-timer.C:
		return "", errors.New("setup: timed out waiting for the GitHub confirmation")
	case <-ctx.Done():
		return "", fmt.Errorf("setup: %w", ctx.Err())
	}
}
