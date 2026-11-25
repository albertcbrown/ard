package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ErrCannotScope means the credentials cannot mint scoped tokens.
var ErrCannotScope = errors.New("github: a personal access token cannot be scoped down; the k8s executor needs GitHub App auth")

// ErrNotInInstallation means a requested repository is not covered by the
// App installation.
var ErrNotInInstallation = errors.New("github: repository is not in the app installation")

// ScopedToken is a short-lived installation token. Its value never reaches a
// log or an error message through fmt or slog.
type ScopedToken struct {
	Value     string
	ExpiresAt time.Time
}

const redactedToken = "[token]"

// String hides the value.
func (ScopedToken) String() string { return redactedToken }

// GoString hides the value for %#v.
func (ScopedToken) GoString() string { return redactedToken }

// LogValue hides the value from slog.
func (ScopedToken) LogValue() slog.Value { return slog.StringValue(redactedToken) }

// ScopedMinter returns a as an *AppAuth, or ErrCannotScope for any other kind.
func ScopedMinter(a Auth) (*AppAuth, error) {
	if app, ok := a.(*AppAuth); ok && app != nil {
		return app, nil
	}
	return nil, ErrCannotScope
}

func (a *AppAuth) apiBase() (http.RoundTripper, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	base := a.base
	if base == nil {
		base = http.DefaultTransport
	}
	u := a.baseURL
	if u == "" {
		u = defaultBaseURL
	}
	return base, strings.TrimRight(u, "/")
}

// MintReadToken mints an installation token limited to contents:read on repos.
// Every repo must belong to the installation's owner; otherwise no request is made.
func (a *AppAuth) MintReadToken(ctx context.Context, repos []Repo) (ScopedToken, error) {
	if len(repos) == 0 {
		return ScopedToken{}, errors.New("github: no repositories to scope the token to")
	}
	a.mu.Lock()
	owner := a.repo.Owner
	a.mu.Unlock()
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		if r.Name == "" || !strings.EqualFold(r.Owner, owner) {
			return ScopedToken{}, fmt.Errorf("%w: %s", ErrNotInInstallation, r)
		}
		names = append(names, r.Name)
	}
	id, err := a.installationID(ctx)
	if err != nil {
		return ScopedToken{}, err
	}
	base, root := a.apiBase()
	jwt, err := a.appsTransport(base)
	if err != nil {
		return ScopedToken{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"repositories": names,
		"permissions":  map[string]string{"contents": "read"},
	})
	if err != nil {
		return ScopedToken{}, err
	}
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", root, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return ScopedToken{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := newAPIClient(jwt).Do(req)
	if err != nil {
		return ScopedToken{}, fmt.Errorf("github: mint scoped token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusCreated:
	case http.StatusUnprocessableEntity:
		return ScopedToken{}, fmt.Errorf("%w: %s", ErrNotInInstallation, errorMessage(body))
	default:
		return ScopedToken{}, fmt.Errorf("github: mint scoped token: %w", statusError(resp.StatusCode, body))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return ScopedToken{}, errors.New("github: mint scoped token: unexpected response")
	}
	return ScopedToken{Value: out.Token, ExpiresAt: out.ExpiresAt}, nil
}

// RevokeToken revokes an installation token. An already revoked or expired
// token (401) counts as success.
func (a *AppAuth) RevokeToken(ctx context.Context, tok string) error {
	base, root := a.apiBase()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, root+"/installation/token", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := newAPIClient(base).Do(req)
	if err != nil {
		return fmt.Errorf("github: revoke token: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusUnauthorized:
		return nil
	}
	return fmt.Errorf("github: revoke token: %w", statusError(resp.StatusCode, body))
}
