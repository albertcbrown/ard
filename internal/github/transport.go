package github

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const maxAttempts = 4

// readOnlyTransport refuses every request that could mutate GitHub. It sits
// above auth so that ghinstallation's own token-mint POST (made on its inner
// transport) is unaffected.
type readOnlyTransport struct{ next http.RoundTripper }

func (t readOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		return t.next.RoundTrip(req)
	}
	if req.Body != nil {
		req.Body.Close()
	}
	return nil, ErrReadOnly
}

// retryTransport handles rate limits, transient failures and search spacing.
type retryTransport struct {
	next    http.RoundTripper
	maxWait time.Duration
	log     *slog.Logger
	// injectable for tests
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	// jitter returns a duration in [0, d/2].
	jitter func(d time.Duration) time.Duration
	// search spaces /search/ requests under GitHub's 30 per minute limit.
	search *rate.Limiter
}

func newRetryTransport(next http.RoundTripper, maxWait time.Duration, log *slog.Logger) *retryTransport {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &retryTransport{
		next:    next,
		maxWait: maxWait,
		log:     log,
		now:     time.Now,
		sleep:   sleepCtx,
		jitter: func(d time.Duration) time.Duration {
			return time.Duration(rand.Int64N(int64(d/2) + 1))
		},
		search: rate.NewLimiter(rate.Every(2*time.Second), 5),
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if strings.HasPrefix(req.URL.Path, "/search/") {
		if err := t.search.Wait(ctx); err != nil {
			return nil, err
		}
	}
	idempotent := req.Method == http.MethodGet || req.Method == http.MethodHead
	rewindable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	secondaryWaits := 0
	var serverBackoff time.Duration = time.Second

	for attempt := 1; ; attempt++ {
		r := req
		if attempt > 1 && req.GetBody != nil && req.Body != nil && req.Body != http.NoBody {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r = req.Clone(ctx)
			r.Body = body
		}
		start := t.now()
		resp, err := t.next.RoundTrip(r)
		dur := t.now().Sub(start)
		last := attempt >= maxAttempts

		if err != nil {
			t.log.Debug("github request", "method", req.Method, "path", req.URL.Path, "err", err.Error(), "attempt", attempt)
			if !idempotent || last || ctx.Err() != nil || !isTimeout(err) {
				return nil, err
			}
			d := serverBackoff + t.jitter(serverBackoff)
			serverBackoff *= 2
			t.log.Warn("github request timed out; retrying", "path", req.URL.Path, "wait", d.String())
			if err := t.sleep(ctx, d); err != nil {
				return nil, err
			}
			continue
		}
		t.log.Debug("github request", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode,
			"duration", dur.String(), "ratelimit_remaining", resp.Header.Get("X-RateLimit-Remaining"), "attempt", attempt)

		switch {
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if rerr != nil {
				return nil, rerr
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			rl, ok := classifyRateLimit(resp.StatusCode, resp.Header, body, t.now(), secondaryWaits)
			if !ok {
				return resp, nil
			}
			if !rewindable {
				return resp, nil
			}
			if rl.wait > t.maxWait || last {
				return nil, &RateLimitError{ResetAt: t.now().Add(rl.wait), Secondary: rl.secondary}
			}
			if rl.secondary && !rl.explicit {
				secondaryWaits++
			}
			t.log.Warn("github rate limited; waiting", "path", req.URL.Path, "wait", rl.wait.String(), "secondary", rl.secondary)
			if err := t.sleep(ctx, rl.wait); err != nil {
				return nil, err
			}
		case idempotent && !last && (resp.StatusCode == 500 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504):
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			d := serverBackoff + t.jitter(serverBackoff)
			serverBackoff *= 2
			t.log.Warn("github server error; retrying", "path", req.URL.Path, "status", resp.StatusCode, "wait", d.String())
			if err := t.sleep(ctx, d); err != nil {
				return nil, err
			}
		default:
			return resp, nil
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

type rateLimit struct {
	wait      time.Duration
	secondary bool
	explicit  bool // wait came from a header rather than a default
}

// classifyRateLimit decides whether a 403/429 is a rate limit and how long to
// wait. secondaryWaits counts earlier header-less secondary waits (for
// doubling).
func classifyRateLimit(status int, h http.Header, body []byte, now time.Time, secondaryWaits int) (rateLimit, bool) {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return rateLimit{}, false
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if reset, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			wait := time.Unix(reset, 0).Sub(now)
			if wait < 0 {
				wait = 0
			}
			return rateLimit{wait: wait + time.Second, explicit: true}, true
		}
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return rateLimit{wait: time.Duration(secs) * time.Second, secondary: true, explicit: true}, true
		}
	}
	if status == http.StatusTooManyRequests || bytes.Contains(bytes.ToLower(body), []byte("secondary rate limit")) {
		return rateLimit{wait: 60 * time.Second << secondaryWaits, secondary: true}, true
	}
	return rateLimit{}, false
}
