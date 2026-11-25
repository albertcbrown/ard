package gitx

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestClassifyTransient(t *testing.T) {
	for _, tc := range []struct {
		stderr string
		want   bool
	}{
		{"remote: Internal Server Error", true},
		{"fatal: unable to access 'x': The requested URL returned error: 502", true},
		{"fatal: unable to access 'x': Could not resolve host: github.com", true},
		{"error: RPC failed; curl 56 Connection reset by peer", true},
		{"fatal: the remote end hung up unexpectedly\nfatal: early EOF", true},
		{"fatal: unable to access 'x': Failed to connect to github.com port 443", true},
		{"error: failed to push some refs (stale info)", false},
		{"The requested URL returned error: 403", false},
		{"remote: error: GH006: Protected branch update failed", false},
	} {
		if got := classifyTransient(tc.stderr); got != tc.want {
			t.Errorf("classifyTransient(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}

func TestClassifyFetchTransient(t *testing.T) {
	if err := classifyFetch("fatal: couldn't find remote ref x", false); !errors.Is(err, ErrRefNotFound) {
		t.Errorf("ref not found: %v", err)
	}
	if err := classifyFetch("error: RPC failed; early EOF", false); !errors.Is(err, ErrTransient) {
		t.Errorf("transient: %v", err)
	}
	if err := classifyFetch("returned error: 403", true); !errors.Is(err, ErrRepoAccess) {
		t.Errorf("403: %v", err)
	}
}

func TestTokenTransient(t *testing.T) {
	if !tokenTransient(&net.DNSError{Err: "no such host", Name: "api.github.com"}) {
		t.Error("DNS error should be transient")
	}
	if tokenTransient(errors.New("bad key")) {
		t.Error("plain error should not be transient")
	}
	g := New("")
	tok := func(context.Context) (string, error) {
		return "", &net.DNSError{Err: "no such host", Name: "api.github.com"}
	}
	err := g.Push(context.Background(), PushOp{Dir: t.TempDir(), Remote: "x", Ref: "HEAD:refs/heads/x", Token: tok})
	if !errors.Is(err, ErrTransient) {
		t.Errorf("Push err = %v", err)
	}
	err = g.FetchRef(context.Background(), t.TempDir(), "x", "+a:b", tok)
	if !errors.Is(err, ErrTransient) {
		t.Errorf("FetchRef err = %v", err)
	}
}
