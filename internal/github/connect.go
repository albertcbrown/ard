package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Connection is what Connect learned about the credentials.
type Connection struct {
	Kind        AuthKind
	Login       string // token user, or the App's slug with [bot]
	Repo        Repo
	Permissions map[string]string // permission name to level (read, write); a token's repo flags map to contents-style levels
	Scopes      []string          // X-OAuth-Scopes of a classic PAT
}

// appNeeds lists the App permissions ard needs and the level each requires.
var appNeeds = []struct{ name, level string }{
	{"contents", "write"}, {"issues", "write"}, {"pull_requests", "write"},
	{"checks", "read"}, {"statuses", "read"},
}

func levelOK(have, need string) bool {
	switch have {
	case "admin", "write":
		return true
	case "read":
		return need == "read"
	}
	return false
}

// Connect checks cfg's credentials against the repo before any loop starts. It
// returns a *ConnectError naming what is missing. With cfg.ReadOnly only read
// access is required. Errors never carry a credential or a response body.
func Connect(ctx context.Context, cfg Config) (Connection, error) {
	if cfg.Auth == nil {
		return Connection{}, &ConnectError{ConnectNoCredentials, "no GitHub credentials configured"}
	}
	base := http.DefaultTransport
	if cfg.HTTPClient != nil && cfg.HTTPClient.Transport != nil {
		base = cfg.HTTPClient.Transport
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	c := &connector{cfg: cfg, base: base, baseURL: baseURL}
	if app, ok := cfg.Auth.(*AppAuth); ok {
		return c.app(ctx, app)
	}
	return c.token(ctx)
}

type connector struct {
	cfg     Config
	base    http.RoundTripper
	baseURL string
}

// get fetches path with rt. It returns the status, headers and a bounded body.
func (c *connector) get(ctx context.Context, rt http.RoundTripper, path string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ard")
	hc := &http.Client{Transport: rt, Timeout: 30 * time.Second, CheckRedirect: refuseRedirect}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, resp.Header, body, nil
}

func unreachable(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &ConnectError{ConnectUnreachable, "cannot reach the GitHub API"}
}

func (c *connector) token(ctx context.Context) (Connection, error) {
	repo := c.cfg.Repo
	rt, err := c.cfg.Auth.Transport(c.base)
	if err != nil {
		return Connection{}, &ConnectError{ConnectNoCredentials, "no GitHub token configured"}
	}
	status, hdr, body, err := c.get(ctx, rt, "/repos/"+repo.String())
	if err != nil {
		return Connection{}, unreachable(ctx)
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return Connection{}, &ConnectError{ConnectUnauthorized, "GitHub rejected the token (bad or expired)"}
	case http.StatusNotFound:
		return Connection{}, &ConnectError{ConnectRepoNotFound, fmt.Sprintf("repository %s not found, or the token cannot see it", repo)}
	default:
		return Connection{}, &ConnectError{ConnectUnreachable, fmt.Sprintf("GET /repos/%s returned HTTP %d", repo, status)}
	}
	var r struct {
		Private     bool            `json:"private"`
		Permissions map[string]bool `json:"permissions"`
	}
	if json.Unmarshal(body, &r) != nil {
		return Connection{}, &ConnectError{ConnectUnreachable, "unexpected response for the repository"}
	}
	conn := Connection{Kind: AuthToken, Repo: repo, Permissions: map[string]string{}}
	switch {
	case r.Permissions["admin"] || r.Permissions["maintain"] || r.Permissions["push"]:
		conn.Permissions["contents"], conn.Permissions["issues"], conn.Permissions["pull_requests"] = "write", "write", "write"
	case r.Permissions["pull"]:
		conn.Permissions["contents"], conn.Permissions["issues"], conn.Permissions["pull_requests"] = "read", "read", "read"
	}
	if s := strings.TrimSpace(hdr.Get("X-OAuth-Scopes")); s != "" {
		for _, p := range strings.Split(s, ",") {
			conn.Scopes = append(conn.Scopes, strings.TrimSpace(p))
		}
	}
	var missing []string
	if c.cfg.ReadOnly {
		if conn.Permissions["contents"] == "" {
			missing = []string{"contents read", "issues read", "pull requests read"}
		}
	} else if conn.Permissions["contents"] != "write" {
		missing = []string{"contents write", "issues write", "pull requests write"}
	}
	if len(missing) == 0 && len(conn.Scopes) > 0 && !c.cfg.ReadOnly && !hasScope(conn.Scopes, "repo", !r.Private) {
		missing = []string{"the repo scope (token scopes: " + strings.Join(conn.Scopes, ", ") + ")"}
	}
	if len(missing) > 0 {
		return Connection{}, &ConnectError{ConnectMissingPermission, "token lacks " + strings.Join(missing, ", ")}
	}
	if len(conn.Scopes) == 0 {
		// A fine-grained token lists no permissions: probe issues and pull requests.
		for _, p := range []struct{ path, name string }{{"/issues", "issues"}, {"/pulls", "pull requests"}} {
			st, _, _, err := c.get(ctx, rt, "/repos/"+repo.String()+p.path+"?per_page=1")
			if err != nil {
				return Connection{}, unreachable(ctx)
			}
			if st != http.StatusOK {
				return Connection{}, &ConnectError{ConnectMissingPermission, "token lacks " + p.name + " access"}
			}
		}
	}
	st, _, ub, err := c.get(ctx, rt, "/user")
	if err != nil {
		return Connection{}, unreachable(ctx)
	}
	if st == http.StatusOK {
		var u struct {
			Login string `json:"login"`
		}
		_ = json.Unmarshal(ub, &u)
		conn.Login = u.Login
	}
	return conn, nil
}

// hasScope reports whether scopes include want, or public_repo for a public repo.
func hasScope(scopes []string, want string, public bool) bool {
	for _, s := range scopes {
		if s == want || (public && s == "public_repo") {
			return true
		}
	}
	return false
}

func (c *connector) app(ctx context.Context, app *AppAuth) (Connection, error) {
	repo := c.cfg.Repo
	app.configure(repo, c.baseURL)
	jwt, err := app.appsTransport(c.base)
	if err != nil {
		return Connection{}, &ConnectError{ConnectBadKey, "the GitHub App private key is unusable"}
	}
	status, _, body, err := c.get(ctx, jwt, "/repos/"+repo.String()+"/installation")
	if err != nil {
		return Connection{}, unreachable(ctx)
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return Connection{}, &ConnectError{ConnectBadKey, "GitHub rejected the App credentials (wrong app ID or private key)"}
	case http.StatusNotFound:
		return Connection{}, &ConnectError{ConnectAppNotInstalled, fmt.Sprintf("the GitHub App is not installed on %s", repo)}
	default:
		return Connection{}, &ConnectError{ConnectUnreachable, fmt.Sprintf("GET /repos/%s/installation returned HTTP %d", repo, status)}
	}
	var in struct {
		AppSlug     string            `json:"app_slug"`
		Permissions map[string]string `json:"permissions"`
	}
	if json.Unmarshal(body, &in) != nil {
		return Connection{}, &ConnectError{ConnectUnreachable, "unexpected response for the installation"}
	}
	conn := Connection{Kind: AuthApp, Repo: repo, Permissions: in.Permissions}
	if in.AppSlug != "" {
		conn.Login = in.AppSlug + "[bot]"
	}
	var missing []string
	for _, n := range appNeeds {
		need := n.level
		if c.cfg.ReadOnly {
			need = "read"
		}
		if !levelOK(in.Permissions[n.name], need) {
			missing = append(missing, strings.ReplaceAll(n.name, "_", " ")+" "+need)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return Connection{}, &ConnectError{ConnectMissingPermission, "the GitHub App installation lacks " + strings.Join(missing, ", ")}
	}
	// Prove the installation token works and the repo is reachable.
	rt, err := app.Transport(c.base)
	if err != nil {
		return Connection{}, &ConnectError{ConnectBadKey, "the GitHub App private key is unusable"}
	}
	st, _, _, err := c.get(ctx, rt, "/repos/"+repo.String())
	if err != nil {
		return Connection{}, unreachable(ctx)
	}
	if st == http.StatusNotFound {
		return Connection{}, &ConnectError{ConnectRepoNotFound, fmt.Sprintf("repository %s not found for the App installation", repo)}
	}
	if st != http.StatusOK {
		return Connection{}, &ConnectError{ConnectUnauthorized, fmt.Sprintf("GET /repos/%s returned HTTP %d", repo, st)}
	}
	return conn, nil
}
