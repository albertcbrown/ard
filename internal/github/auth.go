package github

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gogh "github.com/google/go-github/v88/github"
)

// EnvNames lists every environment variable that carries GitHub credentials.
// The executor (#18) strips these from every agent environment (invariant 2).
var EnvNames = []string{
	"ARD_GITHUB_APP_ID",
	"ARD_GITHUB_INSTALLATION_ID",
	"ARD_GITHUB_PRIVATE_KEY",
	"ARD_GITHUB_PRIVATE_KEY_FILE",
	"ARD_GITHUB_TOKEN",
}

// Auth supplies credentials. Both a GitHub App and a personal access token
// work for live runs; `ard serve` checks them with Connect at startup.
type Auth interface {
	Kind() AuthKind
	// Transport wraps base so requests carry credentials. It makes no request.
	Transport(base http.RoundTripper) (http.RoundTripper, error)
	// Token returns a fresh token, for git push (#17).
	Token(ctx context.Context) (string, error)
}

// AppAuth authenticates as a GitHub App installation. InstallationID 0 means
// discover it via GET /repos/{owner}/{repo}/installation on first use.
type AppAuth struct {
	AppID          int64
	InstallationID int64
	PrivateKey     []byte

	mu      sync.Mutex
	base    http.RoundTripper
	baseURL string
	repo    Repo
	inst    *ghinstallation.Transport
	instID  int64 // configured or discovered installation id
}

// configure is called by New with the repo and API base URL.
func (a *AppAuth) configure(repo Repo, baseURL string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.repo = repo
	a.baseURL = strings.TrimRight(baseURL, "/")
}

func (a *AppAuth) Kind() AuthKind { return AuthApp }

// Transport returns a lazy round tripper: the installation is discovered and
// its token minted on first use, and ghinstallation refreshes the token before
// it expires.
func (a *AppAuth) Transport(base http.RoundTripper) (http.RoundTripper, error) {
	if a.AppID == 0 || len(a.PrivateKey) == 0 {
		return nil, errors.New("github: app auth needs an app ID and a private key")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	a.mu.Lock()
	a.base = base
	a.mu.Unlock()
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t, err := a.installation(req.Context())
		if err != nil {
			return nil, err
		}
		return t.RoundTrip(req)
	}), nil
}

// Token returns the current installation token, minting one if needed.
func (a *AppAuth) Token(ctx context.Context) (string, error) {
	t, err := a.installation(ctx)
	if err != nil {
		return "", err
	}
	tok, err := t.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("github: installation token: %w", err)
	}
	return tok, nil
}

func (a *AppAuth) installation(ctx context.Context) (*ghinstallation.Transport, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inst != nil {
		return a.inst, nil
	}
	base := a.base
	if base == nil {
		base = http.DefaultTransport
	}
	id := a.InstallationID
	if id == 0 {
		var err error
		if id, err = a.discover(ctx, base); err != nil {
			return nil, err
		}
	}
	a.instID = id
	t, err := ghinstallation.New(base, a.AppID, id, a.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("github: app transport: %w", err)
	}
	if a.baseURL != "" {
		t.BaseURL = a.baseURL
	}
	// Token refresh must not follow redirects either.
	t.Client = newAPIClient(base)
	a.inst = t
	return t, nil
}

// installationID returns the installation id, discovering it when unknown.
func (a *AppAuth) installationID(ctx context.Context) (int64, error) {
	a.mu.Lock()
	id := a.instID
	a.mu.Unlock()
	if id != 0 {
		return id, nil
	}
	if _, err := a.installation(ctx); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.instID, nil
}

func (a *AppAuth) discover(ctx context.Context, base http.RoundTripper) (int64, error) {
	if a.repo == (Repo{}) {
		return 0, errors.New("github: cannot discover installation without a repo")
	}
	jwt, err := a.appsTransport(base)
	if err != nil {
		return 0, err
	}
	url := a.baseURL
	if url == "" {
		url = defaultBaseURL
	}
	url = strings.TrimRight(url, "/") + "/repos/" + a.repo.String() + "/installation"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := newAPIClient(jwt).Do(req)
	if err != nil {
		return 0, fmt.Errorf("github: discover installation: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		// The JWT cannot read repo endpoints, so the new name is not resolved.
		return 0, &RepoMovedError{Repo: a.repo, Location: locationPath(resp.Header.Get("Location"))}
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("github: discover installation for %s: %w", a.repo, statusError(resp.StatusCode, body))
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == 0 {
		return 0, fmt.Errorf("github: discover installation for %s: unexpected response", a.repo)
	}
	return out.ID, nil
}

// locationPath returns the path of a Location header, never its query or host.
func locationPath(loc string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return "<none>"
	}
	return redirectPath(u)
}

// appsTransport signs requests with the App JWT (for /app and installation lookup).
func (a *AppAuth) appsTransport(base http.RoundTripper) (http.RoundTripper, error) {
	t, err := ghinstallation.NewAppsTransport(base, a.AppID, a.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("github: app JWT transport: %w", err)
	}
	if a.baseURL != "" {
		t.BaseURL = a.baseURL
	}
	return t, nil
}

// TokenAuth is a personal access token. Actions are taken as the token's user.
// Source says where it came from ("env" or "gh"). It never formats its value.
type TokenAuth struct {
	Value  string
	Source string
}

// String and GoString keep the token out of %v, %+v, %#v and %s output.
func (a *TokenAuth) String() string { return "token(" + a.sourceName() + ")" }

// GoString is the %#v form; see String.
func (a *TokenAuth) GoString() string { return a.String() }

func (a *TokenAuth) sourceName() string {
	if a == nil || a.Source == "" {
		return "env"
	}
	return a.Source
}

// NewTokenAuth wraps a token.
func NewTokenAuth(token string) *TokenAuth { return &TokenAuth{Value: token} }

func (*TokenAuth) Kind() AuthKind { return AuthToken }

func (a *TokenAuth) Transport(base http.RoundTripper) (http.RoundTripper, error) {
	if a.Value == "" {
		return nil, ErrNoCredentials
	}
	if base == nil {
		base = http.DefaultTransport
	}
	tok := a.Value
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		r := req.Clone(req.Context())
		r.Header.Set("Authorization", "Bearer "+tok)
		return base.RoundTrip(r)
	}), nil
}

func (a *TokenAuth) Token(context.Context) (string, error) { return a.Value, nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AuthFromEnv builds credentials from ARD_GITHUB_* variables. GH_TOKEN is
// deliberately not read, so a personal token is never picked up implicitly.
// Error messages name variables but never echo their values.
func AuthFromEnv(getenv func(string) string, readFile func(string) ([]byte, error)) (Auth, error) {
	var (
		appID   = strings.TrimSpace(getenv("ARD_GITHUB_APP_ID"))
		instID  = strings.TrimSpace(getenv("ARD_GITHUB_INSTALLATION_ID"))
		keyEnv  = getenv("ARD_GITHUB_PRIVATE_KEY")
		keyFile = strings.TrimSpace(getenv("ARD_GITHUB_PRIVATE_KEY_FILE"))
		token   = strings.TrimSpace(getenv("ARD_GITHUB_TOKEN"))
	)
	anyApp := appID != "" || instID != "" || keyEnv != "" || keyFile != ""
	switch {
	case anyApp && token != "":
		return nil, errors.New("github: both GitHub App variables and ARD_GITHUB_TOKEN are set; use one")
	case !anyApp && token == "":
		return nil, ErrNoCredentials
	case !anyApp:
		return &TokenAuth{Value: token, Source: "env"}, nil
	}
	if appID == "" {
		return nil, errors.New("github: ARD_GITHUB_APP_ID is required for GitHub App auth")
	}
	id, err := strconv.ParseInt(appID, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("github: ARD_GITHUB_APP_ID must be a positive integer")
	}
	var inst int64
	if instID != "" {
		if inst, err = strconv.ParseInt(instID, 10, 64); err != nil || inst <= 0 {
			return nil, errors.New("github: ARD_GITHUB_INSTALLATION_ID must be a positive integer")
		}
	}
	if keyEnv != "" && keyFile != "" {
		return nil, errors.New("github: set only one of ARD_GITHUB_PRIVATE_KEY and ARD_GITHUB_PRIVATE_KEY_FILE")
	}
	var key []byte
	switch {
	case keyFile != "":
		if key, err = readFile(keyFile); err != nil {
			return nil, fmt.Errorf("github: read ARD_GITHUB_PRIVATE_KEY_FILE: %w", err)
		}
	case keyEnv != "":
		key = []byte(keyEnv)
		if !strings.Contains(keyEnv, "\n") {
			key = []byte(strings.ReplaceAll(keyEnv, `\n`, "\n"))
		}
	default:
		return nil, errors.New("github: ARD_GITHUB_PRIVATE_KEY or ARD_GITHUB_PRIVATE_KEY_FILE is required for GitHub App auth")
	}
	if err := checkPEM(key); err != nil {
		return nil, err
	}
	return &AppAuth{AppID: id, InstallationID: inst, PrivateKey: key}, nil
}

func checkPEM(key []byte) error {
	block, _ := pem.Decode(key)
	if block == nil {
		return errors.New("github: GitHub App private key is not valid PEM")
	}
	if _, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return nil
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return nil
	}
	return errors.New("github: GitHub App private key is not a parseable RSA key")
}

// DiscoverInstallation returns the id of this App's installation on repo,
// or an error while it is not installed. baseURL may be empty for
// api.github.com.
func (a *AppAuth) DiscoverInstallation(ctx context.Context, repo Repo, baseURL string) (int64, error) {
	a.configure(repo, baseURL)
	return a.discover(ctx, http.DefaultTransport)
}

// DefaultAPIBaseURL is the public GitHub API root.
func DefaultAPIBaseURL() string { return defaultBaseURL }

// IsTransientToken reports whether a token mint or refresh error comes from the
// network or a 5xx response, not from a refusal.
func IsTransientToken(err error) bool {
	var ne net.Error
	var sc interface{ StatusCode() int }
	var he *ghinstallation.HTTPError
	var er *gogh.ErrorResponse
	switch {
	case err == nil:
		return false
	case errors.As(err, &ne), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	case errors.As(err, &he):
		return he.Response != nil && he.Response.StatusCode >= 500
	case errors.As(err, &er):
		return er.Response != nil && er.Response.StatusCode >= 500
	case errors.As(err, &sc):
		return sc.StatusCode() >= 500
	}
	return false
}
