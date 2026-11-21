package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seq serves scripted responses in order and records requests.
type seq struct {
	mu     sync.Mutex
	steps  []func(w http.ResponseWriter)
	bodies []string
	paths  []string
}

func (s *seq) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	s.bodies = append(s.bodies, string(b))
	s.paths = append(s.paths, r.URL.Path)
	i := len(s.paths) - 1
	if i >= len(s.steps) {
		i = len(s.steps) - 1
	}
	s.steps[i](w)
}

func status(code int, hdr ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(hdr); i += 2 {
			w.Header().Set(hdr[i], hdr[i+1])
		}
		w.WriteHeader(code)
		fmt.Fprint(w, `{"message":"m"}`)
	}
}

func statusBody(code int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code); fmt.Fprint(w, body) }
}

type rig struct {
	rt     *retryTransport
	srv    *httptest.Server
	sleeps []time.Duration
	seq    *seq
	logs   *bytes.Buffer
}

func newRig(t *testing.T, maxWait time.Duration, steps ...func(http.ResponseWriter)) *rig {
	t.Helper()
	r := &rig{seq: &seq{steps: steps}, logs: &bytes.Buffer{}}
	r.srv = httptest.NewServer(r.seq)
	t.Cleanup(r.srv.Close)
	log := slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.rt = newRetryTransport(http.DefaultTransport, maxWait, log)
	r.rt.now = func() time.Time { return t0 }
	r.rt.sleep = func(ctx context.Context, d time.Duration) error {
		r.sleeps = append(r.sleeps, d)
		return ctx.Err()
	}
	r.rt.jitter = func(time.Duration) time.Duration { return 0 }
	return r
}

func (r *rig) do(t *testing.T, ctx context.Context, method, path, body string) (*http.Response, error) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer SUPERSECRET")
	return r.rt.RoundTrip(req)
}

func TestRetryTransport(t *testing.T) {
	reset := func(d time.Duration) string { return strconv.FormatInt(t0.Add(d).Unix(), 10) }
	tests := []struct {
		name       string
		method     string
		body       string
		steps      []func(http.ResponseWriter)
		maxWait    time.Duration
		wantStatus int
		wantErr    func(error) bool
		wantSleeps []time.Duration
		wantCalls  int
	}{
		{name: "200 passes through", method: "GET", steps: []func(http.ResponseWriter){status(200)}, wantStatus: 200, wantCalls: 1},
		{
			name: "primary 403 waits until reset plus 1s", method: "GET",
			steps:      []func(http.ResponseWriter){status(403, "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", reset(5*time.Second)), status(200)},
			wantStatus: 200, wantSleeps: []time.Duration{6 * time.Second}, wantCalls: 2,
		},
		{
			name: "reset in 1h returns RateLimitError without sleeping", method: "GET",
			steps: []func(http.ResponseWriter){status(403, "X-RateLimit-Remaining", "0", "X-RateLimit-Reset", reset(time.Hour))},
			wantErr: func(err error) bool {
				var rl *RateLimitError
				return errors.As(err, &rl) && !rl.Secondary && IsTransient(err)
			},
			wantCalls: 1,
		},
		{
			name: "429 honours Retry-After", method: "GET",
			steps:      []func(http.ResponseWriter){status(429, "Retry-After", "3"), status(200)},
			wantStatus: 200, wantSleeps: []time.Duration{3 * time.Second}, wantCalls: 2,
		},
		{
			name: "secondary 403 without header sleeps 60s then doubles", method: "GET",
			steps: []func(http.ResponseWriter){
				statusBody(403, `{"message":"You have exceeded a secondary rate limit"}`),
				statusBody(403, `{"message":"You have exceeded a secondary rate limit"}`),
				status(200),
			},
			wantStatus: 200, wantSleeps: []time.Duration{60 * time.Second, 120 * time.Second}, wantCalls: 3,
		},
		{
			name: "plain 403 is returned untouched", method: "GET",
			steps: []func(http.ResponseWriter){statusBody(403, `{"message":"Resource not accessible"}`)}, wantStatus: 403, wantCalls: 1,
		},
		{
			name: "GET 502 four times", method: "GET",
			steps:      []func(http.ResponseWriter){status(502)},
			wantStatus: 502, wantSleeps: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}, wantCalls: 4,
		},
		{
			name: "POST 502 is not retried", method: "POST", body: `{"a":1}`,
			steps: []func(http.ResponseWriter){status(502)}, wantStatus: 502, wantCalls: 1,
		},
		{
			name: "rate-limited POST retried with identical body", method: "POST", body: `{"a":1}`,
			steps:      []func(http.ResponseWriter){status(429, "Retry-After", "1"), status(201)},
			wantStatus: 201, wantSleeps: []time.Duration{time.Second}, wantCalls: 2,
		},
		{
			name: "wait above MaxWait", method: "GET", maxWait: time.Second,
			steps:   []func(http.ResponseWriter){status(429, "Retry-After", "30")},
			wantErr: func(err error) bool { var rl *RateLimitError; return errors.As(err, &rl) && rl.Secondary }, wantCalls: 1,
		},
		{
			name: "attempts exhausted", method: "GET",
			steps:      []func(http.ResponseWriter){status(429, "Retry-After", "1")},
			wantErr:    func(err error) bool { var rl *RateLimitError; return errors.As(err, &rl) },
			wantSleeps: []time.Duration{time.Second, time.Second, time.Second}, wantCalls: 4,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw := tc.maxWait
			if mw == 0 {
				mw = 2 * time.Minute
			}
			r := newRig(t, mw, tc.steps...)
			resp, err := r.do(t, context.Background(), tc.method, "/x", tc.body)
			if tc.wantErr != nil {
				if err == nil || !tc.wantErr(err) {
					t.Fatalf("err = %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != tc.wantStatus {
					t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
				}
			}
			if fmt.Sprint(r.sleeps) != fmt.Sprint(tc.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", r.sleeps, tc.wantSleeps)
			}
			if len(r.seq.paths) != tc.wantCalls {
				t.Errorf("calls = %d, want %d", len(r.seq.paths), tc.wantCalls)
			}
			for _, b := range r.seq.bodies {
				if b != tc.body {
					t.Errorf("body %q differs from %q", b, tc.body)
				}
			}
			if strings.Contains(r.logs.String(), "SUPERSECRET") || strings.Contains(strings.ToLower(r.logs.String()), "authorization") {
				t.Errorf("log leaks credentials:\n%s", r.logs)
			}
		})
	}
}

func TestRetryTransportTransientAfterExhaustedServerErrors(t *testing.T) {
	r := newRig(t, time.Minute, status(502))
	resp, err := r.do(t, context.Background(), "GET", "/x", "")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	err = statusError(resp.StatusCode, b)
	var he *HTTPError
	if !errors.As(err, &he) || !IsTransient(err) {
		t.Errorf("err = %v, want transient HTTPError", err)
	}
}

func TestRetryTransportCancelDuringSleep(t *testing.T) {
	r := newRig(t, time.Minute, status(429, "Retry-After", "5"))
	ctx, cancel := context.WithCancel(context.Background())
	r.rt.sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	_, err := r.do(t, ctx, "GET", "/x", "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if IsTransient(err) {
		t.Error("cancellation must not be transient")
	}
}

func TestRetryTransportSearchSpacing(t *testing.T) {
	r := newRig(t, time.Minute, status(200))
	r.rt.search = rate.NewLimiter(rate.Every(100*time.Millisecond), 5)
	start := time.Now()
	for i := 0; i < 5; i++ {
		resp, err := r.do(t, context.Background(), "GET", "/search/issues", "")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if time.Since(start) > 80*time.Millisecond {
		t.Errorf("burst of 5 was throttled: %v", time.Since(start))
	}
	before := time.Now()
	resp, err := r.do(t, context.Background(), "GET", "/search/issues", "")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if d := time.Since(before); d < 60*time.Millisecond {
		t.Errorf("sixth search did not wait: %v", d)
	}
	// non-search requests are not limited
	before = time.Now()
	resp, _ = r.do(t, context.Background(), "GET", "/repos/o/r", "")
	resp.Body.Close()
	if time.Since(before) > 50*time.Millisecond {
		t.Error("non-search request was throttled")
	}
}

func TestReadOnlyTransport(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	c := &http.Client{Transport: readOnlyTransport{next: http.DefaultTransport}}
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		req, _ := http.NewRequest(m, srv.URL+"/graphql", strings.NewReader("{}"))
		if _, err := c.Do(req); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: err = %v, want ErrReadOnly", m, err)
		}
	}
	if hits != 0 {
		t.Errorf("server saw %d requests", hits)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if hits != 1 {
		t.Errorf("GET hits = %d", hits)
	}
}
