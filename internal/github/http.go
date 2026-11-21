package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	gogh "github.com/google/go-github/v88/github"
)

const defaultBaseURL = "https://api.github.com/"

// Config configures New.
type Config struct {
	Repo       Repo
	Auth       Auth
	BaseURL    string        // default https://api.github.com/; tests point it at httptest
	ReadOnly   bool          // refuse every non-GET request with ErrReadOnly (set under --dry-run)
	MaxWait    time.Duration // longest rate-limit sleep before giving up; default 2m
	Timeout    time.Duration // per HTTP request; default 30s
	Log        *slog.Logger
	HTTPClient *http.Client // base transport for tests; default http.DefaultTransport

	sleep func(ctx context.Context, d time.Duration) error // tests only
}

// HTTP is the real Client, backed by go-github behind a layered transport:
// read-only guard, retry/rate-limit handling, per-attempt timeout, auth.
type HTTP struct {
	repo    Repo
	auth    Auth
	baseURL string
	cfg     Config
	httpc   *http.Client
	gh      *gogh.Client

	idMu sync.Mutex
	id   *Identity
}

var _ Client = (*HTTP)(nil)

// New validates cfg and builds a client. It makes no network call.
func New(cfg Config) (*HTTP, error) {
	if cfg.Repo.Owner == "" || cfg.Repo.Name == "" {
		return nil, errors.New("github: Config.Repo is required")
	}
	if cfg.Auth == nil {
		return nil, ErrNoCredentials
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if !strings.HasSuffix(cfg.BaseURL, "/") {
		cfg.BaseURL += "/"
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 2 * time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	base := http.DefaultTransport
	if cfg.HTTPClient != nil && cfg.HTTPClient.Transport != nil {
		base = cfg.HTTPClient.Transport
	}
	if app, ok := cfg.Auth.(*AppAuth); ok {
		app.configure(cfg.Repo, cfg.BaseURL)
	}
	authRT, err := cfg.Auth.Transport(base)
	if err != nil {
		return nil, fmt.Errorf("github: auth: %w", err)
	}
	h := &HTTP{repo: cfg.Repo, auth: cfg.Auth, baseURL: cfg.BaseURL, cfg: cfg}
	h.httpc = newAPIClient(h.wrap(authRT))
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("github: parse BaseURL: %w", err)
	}
	gc, err := gogh.NewClient(gogh.WithHTTPClient(h.httpc), gogh.WithURLs(&cfg.BaseURL, nil), gogh.WithUserAgent("ard"))
	if err != nil {
		return nil, fmt.Errorf("github: new client: %w", err)
	}
	h.gh = gc
	return h, nil
}

// wrap builds readOnly -> retry -> timeout -> inner.
func (h *HTTP) wrap(inner http.RoundTripper) http.RoundTripper {
	rt := http.RoundTripper(timeoutTransport{next: inner, d: h.cfg.Timeout})
	retry := newRetryTransport(rt, h.cfg.MaxWait, h.cfg.Log)
	if h.cfg.sleep != nil {
		retry.sleep = h.cfg.sleep
	}
	rt = retry
	if h.cfg.ReadOnly {
		rt = readOnlyTransport{next: rt}
	}
	return rt
}

// timeoutTransport bounds each attempt (not the retries or sleeps between them).
type timeoutTransport struct {
	next http.RoundTripper
	d    time.Duration
}

func (t timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.d)
	resp, err := t.next.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// ---- error mapping ----

// httpStatus extracts the HTTP status and message from a go-github error.
func httpStatus(err error) (status int, msg string, ok bool) {
	var er *gogh.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		m := er.Message
		for _, e := range er.Errors {
			switch {
			case e.Message != "":
				m += "; " + e.Message
			case e.Code != "":
				m += "; " + e.Field + " " + e.Code
			}
		}
		return er.Response.StatusCode, m, true
	}
	return 0, "", false
}

func isAlreadyExists(err error) bool {
	var er *gogh.ErrorResponse
	if errors.As(err, &er) {
		for _, e := range er.Errors {
			if e.Code == "already_exists" {
				return true
			}
		}
	}
	return false
}

// mapErr turns a go-github or transport error into ard's error vocabulary and
// adds context. Callers handle operation-specific statuses before calling it.
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var rl *RateLimitError
	if errors.As(err, &rl) || errors.Is(err, ErrReadOnly) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("github: %s: %w", op, err)
	}
	var grl *gogh.RateLimitError
	if errors.As(err, &grl) {
		return fmt.Errorf("github: %s: %w", op, &RateLimitError{ResetAt: grl.Rate.Reset.Time})
	}
	var arl *gogh.AbuseRateLimitError
	if errors.As(err, &arl) {
		e := &RateLimitError{Secondary: true}
		if arl.RetryAfter != nil {
			e.ResetAt = time.Now().Add(*arl.RetryAfter)
		}
		return fmt.Errorf("github: %s: %w", op, e)
	}
	var rerr *gogh.RedirectionError
	if errors.As(err, &rerr) {
		return fmt.Errorf("github: %s: %w", op, &HTTPError{Status: rerr.StatusCode, Message: "unexpected redirect to " + redirectPath(rerr.Location)})
	}
	if status, msg, ok := httpStatus(err); ok {
		switch status {
		case http.StatusUnauthorized:
			return fmt.Errorf("github: %s: %w: %s", op, ErrUnauthorized, msg)
		case http.StatusForbidden:
			return fmt.Errorf("github: %s: %w: %s", op, ErrForbidden, msg)
		case http.StatusNotFound, http.StatusGone:
			return fmt.Errorf("github: %s: %w: %s", op, ErrNotFound, msg)
		}
		return fmt.Errorf("github: %s: %w", op, &HTTPError{Status: status, Message: msg})
	}
	return fmt.Errorf("github: %s: %w", op, err)
}

// refuseRedirect makes every http.Client in this package return a 3xx response
// as it is. Following one would turn a write into a GET, replay it into another
// repository, or carry credentials to another host (the auth headers are added
// inside the transport, so Go's cross-host Authorization strip never applies).
func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newAPIClient returns an http.Client over rt that never follows redirects.
func newAPIClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt, CheckRedirect: refuseRedirect}
}

// fail is mapErr for repo-scoped calls: a refused redirect is classified by
// probing the repository (see "Guardrails" in the proposal). n > 0 means the op
// names an issue or PR number. nil in, nil out.
func (h *HTTP) fail(ctx context.Context, op string, n int, err error) error {
	var rerr *gogh.RedirectionError
	if err == nil || !errors.As(err, &rerr) {
		return mapErr(op, err)
	}
	return fmt.Errorf("github: %s: %w", op, h.classifyRedirect(ctx, n, rerr))
}

// classifyRedirect decides what a refused redirect means. Only positive
// evidence that an issue now lives in another repository reads as not found;
// everything else is an error the caller cannot mistake for "gone".
func (h *HTTP) classifyRedirect(ctx context.Context, n int, rerr *gogh.RedirectionError) error {
	// The probe must not call fail (no recursion).
	repo, _, err := h.gh.Repositories.Get(ctx, h.o(), h.r())
	if err != nil {
		var probe *gogh.RedirectionError
		if errors.As(err, &probe) {
			return h.movedError(ctx, probe)
		}
		pe := mapErr("check repository", err)
		if errors.Is(pe, ErrNotFound) {
			// An outage or permission glitch must not read as "issue gone".
			return &HTTPError{Status: rerr.StatusCode, Message: "unexpected redirect to " + redirectPath(rerr.Location) + "; repository check: " + pe.Error()}
		}
		return pe
	}
	if n > 0 {
		base, _ := url.Parse(h.baseURL)
		id, to, ok := redirectTarget(rerr.Location, base)
		other := (id != 0 && repo.GetID() != 0 && id != repo.GetID()) ||
			(to != (Repo{}) && !strings.EqualFold(to.String(), h.repo.String()))
		if ok && other {
			return fmt.Errorf("%w: issue moved to another repository (%s)", ErrNotFound, redirectPath(rerr.Location))
		}
	}
	return &HTTPError{Status: rerr.StatusCode, Message: "unexpected redirect to " + redirectPath(rerr.Location)}
}

// movedError builds the error for a configured repository that redirects. It
// resolves the new name best-effort and never follows the move.
func (h *HTTP) movedError(ctx context.Context, rerr *gogh.RedirectionError) *RepoMovedError {
	e := &RepoMovedError{Repo: h.repo, Location: redirectPath(rerr.Location)}
	base, _ := url.Parse(h.baseURL)
	id, to, ok := redirectTarget(rerr.Location, base)
	switch {
	case !ok:
	case to != (Repo{}):
		e.To = to
	case id != 0:
		if r, _, err := h.gh.Repositories.GetByID(ctx, id); err == nil {
			if to, err := ParseRepo(r.GetFullName()); err == nil {
				e.To = to
			}
		}
	}
	return e
}

// redirectTarget strips the BaseURL path prefix (GHES "/api/v3") and parses
// "repositories/<id>/..." or "repos/<owner>/<name>/...". ok is false for another
// host or an unknown shape.
func redirectTarget(loc, base *url.URL) (repoID int64, repo Repo, ok bool) {
	if loc == nil || base == nil {
		return 0, Repo{}, false
	}
	if loc.Host != "" && !strings.EqualFold(loc.Host, base.Host) {
		return 0, Repo{}, false
	}
	p := loc.Path
	if prefix := strings.TrimRight(base.Path, "/"); prefix != "" {
		if !strings.HasPrefix(p, prefix+"/") {
			return 0, Repo{}, false
		}
		p = strings.TrimPrefix(p, prefix)
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(parts) >= 2 && parts[0] == "repositories":
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id <= 0 {
			return 0, Repo{}, false
		}
		return id, Repo{}, true
	case len(parts) >= 3 && parts[0] == "repos" && parts[1] != "" && parts[2] != "":
		r, err := ParseRepo(parts[1] + "/" + parts[2])
		if err != nil {
			return 0, Repo{}, false
		}
		return 0, r, true
	}
	return 0, Repo{}, false
}

// redirectPath is the path only, never the query or host, and capped.
func redirectPath(loc *url.URL) string {
	if loc == nil || loc.Path == "" {
		return "<none>"
	}
	p := loc.Path
	if len(p) > 200 {
		p = p[:200]
	}
	return p
}

func is404(err error) bool {
	s, _, ok := httpStatus(err)
	return ok && s == http.StatusNotFound
}

func statusIs(err error, code int) bool {
	s, _, ok := httpStatus(err)
	return ok && s == code
}

// ---- pagination ----

// paginate calls fetch with page 1, 2, ... until the next page is 0 or max
// items are collected (max 0 = unlimited).
func paginate[T any](max int, fetch func(page int) ([]T, int, error)) ([]T, error) {
	var out []T
	for page := 1; page != 0; {
		items, next, err := fetch(page)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if max > 0 && len(out) >= max {
			return out[:max], nil
		}
		page = next
	}
	return out, nil
}

func nextPage(r *gogh.Response) int {
	if r == nil {
		return 0
	}
	return r.NextPage
}

func (h *HTTP) o() string { return h.repo.Owner }
func (h *HTTP) r() string { return h.repo.Name }

// ---- Reader ----

func (h *HTTP) Identity(ctx context.Context) (Identity, error) {
	h.idMu.Lock()
	defer h.idMu.Unlock()
	if h.id != nil {
		return *h.id, nil
	}
	var id Identity
	switch h.auth.Kind() {
	case AuthApp:
		app, ok := h.auth.(*AppAuth)
		if !ok {
			return Identity{}, errors.New("github: identity: unsupported app auth")
		}
		jwtRT, err := app.appsTransport(h.baseTransport())
		if err != nil {
			return Identity{}, err
		}
		jc, err := gogh.NewClient(gogh.WithHTTPClient(newAPIClient(h.wrap(jwtRT))), gogh.WithURLs(&h.baseURL, nil), gogh.WithUserAgent("ard"))
		if err != nil {
			return Identity{}, err
		}
		a, _, err := jc.Apps.Get(ctx, "")
		if err != nil {
			return Identity{}, mapErr("identity: get app", err)
		}
		bot := a.GetSlug() + "[bot]"
		u, _, err := h.gh.Users.Get(ctx, bot)
		if err != nil {
			return Identity{}, mapErr("identity: get bot user", err)
		}
		id = Identity{Login: bot, ID: u.GetID(), Kind: AuthApp}
	default:
		u, _, err := h.gh.Users.Get(ctx, "")
		if err != nil {
			return Identity{}, mapErr("identity: get user", err)
		}
		id = Identity{Login: u.GetLogin(), ID: u.GetID(), Kind: AuthToken}
	}
	h.id = &id
	return id, nil
}

func (h *HTTP) baseTransport() http.RoundTripper {
	if h.cfg.HTTPClient != nil && h.cfg.HTTPClient.Transport != nil {
		return h.cfg.HTTPClient.Transport
	}
	return http.DefaultTransport
}

func (h *HTTP) GetIssue(ctx context.Context, n int) (Issue, error) {
	i, _, err := h.gh.Issues.Get(ctx, h.o(), h.r(), n)
	if err != nil {
		return Issue{}, h.fail(ctx, fmt.Sprintf("get issue %d", n), n, err)
	}
	return issueFrom(i), nil
}

func (h *HTTP) ListIssues(ctx context.Context, o ListIssuesOpts) ([]Issue, error) {
	state := o.State
	if state == "" {
		state = StateOpen
	}
	opts := &gogh.IssueListByRepoOptions{State: string(state), Labels: o.Labels, Since: o.Since}
	opts.ListOptions.PerPage = 100
	var out []Issue
	for page := 1; page != 0; {
		opts.ListOptions.Page = page
		items, resp, err := h.gh.Issues.ListByRepo(ctx, h.o(), h.r(), opts)
		if err != nil {
			return nil, h.fail(ctx, "list issues", 0, err)
		}
		for _, i := range items {
			if i.IsPullRequest() && !o.IncludePRs {
				continue
			}
			out = append(out, issueFrom(i))
			if o.Max > 0 && len(out) >= o.Max {
				return out, nil
			}
		}
		page = nextPage(resp)
	}
	return out, nil
}

func (h *HTTP) SearchIssues(ctx context.Context, q string, o SearchOpts) ([]Issue, error) {
	max := o.Max
	if max <= 0 {
		max = 100
	}
	if max > 1000 {
		max = 1000
	}
	full := "repo:" + h.repo.String() + " " + q
	opts := &gogh.SearchOptions{Sort: o.Sort, Order: o.Order}
	opts.PerPage = 100
	items, err := paginate(max, func(page int) ([]Issue, int, error) {
		opts.Page = page
		res, resp, err := h.gh.Search.Issues(ctx, full, opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]Issue, 0, len(res.Issues))
		for _, i := range res.Issues {
			out = append(out, issueFrom(i))
		}
		return out, nextPage(resp), nil
	})
	if err != nil {
		return nil, h.fail(ctx, "search issues", 0, err)
	}
	return items, nil
}

func (h *HTTP) ListComments(ctx context.Context, n int, since time.Time) ([]Comment, error) {
	opts := &gogh.IssueListCommentsOptions{}
	opts.PerPage = 100
	if !since.IsZero() {
		opts.Since = &since
	}
	items, err := paginate(0, func(page int) ([]Comment, int, error) {
		opts.Page = page
		cs, resp, err := h.gh.Issues.ListComments(ctx, h.o(), h.r(), n, opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]Comment, len(cs))
		for i, c := range cs {
			out[i] = commentFrom(c)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, fmt.Sprintf("list comments %d", n), n, err)
}

func (h *HTTP) ListTimeline(ctx context.Context, n int) ([]TimelineEvent, error) {
	opts := &gogh.ListOptions{PerPage: 100}
	items, err := paginate(0, func(page int) ([]TimelineEvent, int, error) {
		opts.Page = page
		ts, resp, err := h.gh.Issues.ListIssueTimeline(ctx, h.o(), h.r(), n, opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]TimelineEvent, len(ts))
		for i, t := range ts {
			out[i] = timelineFrom(t)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, fmt.Sprintf("list timeline %d", n), n, err)
}

func (h *HTTP) ListLabels(ctx context.Context) ([]Label, error) {
	opts := &gogh.ListOptions{PerPage: 100}
	items, err := paginate(0, func(page int) ([]Label, int, error) {
		opts.Page = page
		ls, resp, err := h.gh.Issues.ListLabels(ctx, h.o(), h.r(), opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]Label, len(ls))
		for i, l := range ls {
			out[i] = labelFrom(l)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, "list labels", 0, err)
}

func (h *HTTP) GetPermission(ctx context.Context, login string) (Permission, error) {
	p, _, err := h.gh.Repositories.GetPermissionLevel(ctx, h.o(), h.r(), login)
	if err != nil {
		if is404(err) {
			return Permission{Level: "none"}, nil
		}
		return Permission{}, h.fail(ctx, "get permission "+login, 0, err)
	}
	return Permission{Level: p.GetPermission(), Role: p.GetRoleName()}, nil
}

func (h *HTTP) GetPR(ctx context.Context, n int) (PullRequest, error) {
	p, _, err := h.gh.PullRequests.Get(ctx, h.o(), h.r(), n)
	if err != nil {
		return PullRequest{}, h.fail(ctx, fmt.Sprintf("get PR %d", n), n, err)
	}
	return prFrom(p), nil
}

func (h *HTTP) ListPRs(ctx context.Context, o ListPRsOpts) ([]PullRequest, error) {
	state := o.State
	if state == "" {
		state = StateOpen
	}
	opts := &gogh.PullRequestListOptions{State: string(state), Base: o.Base}
	if o.Head != "" {
		opts.Head = h.o() + ":" + o.Head
	}
	opts.PerPage = 100
	items, err := paginate(0, func(page int) ([]PullRequest, int, error) {
		opts.Page = page
		ps, resp, err := h.gh.PullRequests.List(ctx, h.o(), h.r(), opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]PullRequest, len(ps))
		for i, p := range ps {
			out[i] = prFrom(p)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, "list PRs", 0, err)
}

func (h *HTTP) ListPRFiles(ctx context.Context, n int) ([]PRFile, error) {
	opts := &gogh.ListOptions{PerPage: 100}
	items, err := paginate(0, func(page int) ([]PRFile, int, error) {
		opts.Page = page
		fs, resp, err := h.gh.PullRequests.ListFiles(ctx, h.o(), h.r(), n, opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]PRFile, len(fs))
		for i, f := range fs {
			out[i] = prFileFrom(f)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, fmt.Sprintf("list PR files %d", n), n, err)
}

func (h *HTTP) ListReviewComments(ctx context.Context, n int) ([]ReviewComment, error) {
	opts := &gogh.PullRequestListCommentsOptions{}
	opts.PerPage = 100
	items, err := paginate(0, func(page int) ([]ReviewComment, int, error) {
		opts.Page = page
		cs, resp, err := h.gh.PullRequests.ListComments(ctx, h.o(), h.r(), n, opts)
		if err != nil {
			return nil, 0, err
		}
		out := make([]ReviewComment, len(cs))
		for i, c := range cs {
			out[i] = reviewCommentFrom(c)
		}
		return out, nextPage(resp), nil
	})
	return items, h.fail(ctx, fmt.Sprintf("list review comments %d", n), n, err)
}

// ---- Writer ----

func (h *HTTP) CreateIssue(ctx context.Context, in NewIssue) (Issue, error) {
	req := &gogh.IssueRequest{Title: &in.Title, Body: &in.Body}
	if len(in.Labels) > 0 {
		req.Labels = &in.Labels
	}
	i, _, err := h.gh.Issues.Create(ctx, h.o(), h.r(), req)
	if err != nil {
		return Issue{}, h.fail(ctx, "create issue", 0, err)
	}
	return issueFrom(i), nil
}

func (h *HTTP) UpdateIssue(ctx context.Context, n int, u IssueUpdate) error {
	req := &gogh.IssueRequest{Title: u.Title, Body: u.Body}
	if u.State != nil {
		s := string(*u.State)
		req.State = &s
	}
	if u.StateReason != nil {
		s := string(*u.StateReason)
		req.StateReason = &s
	}
	_, _, err := h.gh.Issues.Edit(ctx, h.o(), h.r(), n, req)
	return h.fail(ctx, fmt.Sprintf("update issue %d", n), n, err)
}

func (h *HTTP) PinIssue(ctx context.Context, n int) error {
	is, err := h.GetIssue(ctx, n)
	if err != nil {
		return err
	}
	return mapErr(fmt.Sprintf("pin issue %d", n), h.graphql(ctx, pinIssueMutation, map[string]any{"id": is.NodeID}, nil))
}

func (h *HTTP) AddLabels(ctx context.Context, n int, labels ...string) error {
	if len(labels) == 0 {
		return nil
	}
	_, _, err := h.gh.Issues.AddLabelsToIssue(ctx, h.o(), h.r(), n, labels)
	return h.fail(ctx, fmt.Sprintf("add labels %d", n), n, err)
}

func (h *HTTP) RemoveLabel(ctx context.Context, n int, label string) error {
	_, err := h.gh.Issues.RemoveLabelForIssue(ctx, h.o(), h.r(), n, label)
	if err != nil && is404(err) {
		return nil
	}
	return h.fail(ctx, fmt.Sprintf("remove label %d", n), n, err)
}

func (h *HTTP) CreateLabel(ctx context.Context, l Label) error {
	_, _, err := h.gh.Issues.CreateLabel(ctx, h.o(), h.r(), &gogh.Label{Name: &l.Name, Color: &l.Color, Description: &l.Description})
	if err != nil && isAlreadyExists(err) {
		return fmt.Errorf("github: create label %q: %w", l.Name, ErrConflict)
	}
	return h.fail(ctx, fmt.Sprintf("create label %q", l.Name), 0, err)
}

func (h *HTTP) UpdateLabel(ctx context.Context, name string, l Label) error {
	if l.Name == "" {
		l.Name = name
	}
	_, _, err := h.gh.Issues.EditLabel(ctx, h.o(), h.r(), name, &gogh.Label{Name: &l.Name, Color: &l.Color, Description: &l.Description})
	return h.fail(ctx, fmt.Sprintf("update label %q", name), 0, err)
}

func (h *HTTP) CreateComment(ctx context.Context, n int, body string) (Comment, error) {
	c, _, err := h.gh.Issues.CreateComment(ctx, h.o(), h.r(), n, &gogh.IssueComment{Body: &body})
	if err != nil {
		return Comment{}, h.fail(ctx, fmt.Sprintf("create comment %d", n), n, err)
	}
	return commentFrom(c), nil
}

func (h *HTTP) CreatePR(ctx context.Context, in NewPR) (PullRequest, error) {
	req := &gogh.NewPullRequest{Title: &in.Title, Body: &in.Body, Head: &in.Head, Base: &in.Base}
	if in.Draft {
		req.Draft = &in.Draft
	}
	p, _, err := h.gh.PullRequests.Create(ctx, h.o(), h.r(), req)
	if err != nil {
		return PullRequest{}, h.fail(ctx, "create PR", 0, err)
	}
	return prFrom(p), nil
}

func (h *HTTP) UpdatePR(ctx context.Context, n int, u PRUpdate) error {
	_, _, err := h.gh.PullRequests.Edit(ctx, h.o(), h.r(), n, &gogh.PullRequest{Title: u.Title, Body: u.Body})
	return h.fail(ctx, fmt.Sprintf("update PR %d", n), n, err)
}

func (h *HTTP) MarkReady(ctx context.Context, n int) error {
	pr, err := h.GetPR(ctx, n)
	if err != nil {
		return err
	}
	return mapErr(fmt.Sprintf("mark PR %d ready", n), h.graphql(ctx, markReadyMutation, map[string]any{"id": pr.NodeID}, nil))
}

func (h *HTTP) CreateReview(ctx context.Context, n int, in ReviewInput) error {
	ev := "COMMENT"
	req := &gogh.PullRequestReviewRequest{Event: &ev}
	if in.Body != "" {
		req.Body = &in.Body
	}
	if in.CommitSHA != "" {
		req.CommitID = &in.CommitSHA
	}
	for _, c := range in.Comments {
		side := c.Side
		if side == "" {
			side = "RIGHT"
		}
		path, line, body := c.Path, c.Line, c.Body
		req.Comments = append(req.Comments, &gogh.DraftReviewComment{Path: &path, Line: &line, Side: &side, Body: &body})
	}
	_, _, err := h.gh.PullRequests.CreateReview(ctx, h.o(), h.r(), n, req)
	return h.fail(ctx, fmt.Sprintf("create review %d", n), n, err)
}

func (h *HTTP) UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error {
	_, _, err := h.gh.PullRequests.UpdateBranch(ctx, h.o(), h.r(), n, &gogh.PullRequestBranchUpdateOptions{ExpectedHeadSHA: &expectedHeadSHA})
	if err != nil {
		var accepted *gogh.AcceptedError
		if errors.As(err, &accepted) {
			return nil // 202: the update was queued
		}
		if statusIs(err, http.StatusUnprocessableEntity) {
			return fmt.Errorf("github: update branch %d: %w", n, ErrConflict)
		}
	}
	return h.fail(ctx, fmt.Sprintf("update branch %d", n), n, err)
}

func (h *HTTP) MergePR(ctx context.Context, n int, in MergeInput) (MergeResult, error) {
	if in.SHA == "" {
		return MergeResult{}, fmt.Errorf("github: merge PR %d: head SHA is required", n)
	}
	res, _, err := h.gh.PullRequests.Merge(ctx, h.o(), h.r(), n, in.Message,
		&gogh.PullRequestOptions{CommitTitle: in.Title, SHA: in.SHA, MergeMethod: "squash"})
	if err != nil {
		switch {
		case statusIs(err, http.StatusMethodNotAllowed):
			return MergeResult{}, fmt.Errorf("github: merge PR %d: %w", n, ErrNotMergeable)
		case statusIs(err, http.StatusConflict):
			return MergeResult{}, fmt.Errorf("github: merge PR %d: %w", n, ErrConflict)
		}
		return MergeResult{}, h.fail(ctx, fmt.Sprintf("merge PR %d", n), n, err)
	}
	if !res.GetMerged() {
		return MergeResult{}, fmt.Errorf("github: merge PR %d: %w: %s", n, ErrNotMergeable, res.GetMessage())
	}
	return MergeResult{SHA: res.GetSHA()}, nil
}

func (h *HTTP) DeleteBranch(ctx context.Context, branch string) error {
	_, err := h.gh.Git.DeleteRef(ctx, h.o(), h.r(), "heads/"+branch)
	if err != nil && (is404(err) || statusIs(err, http.StatusUnprocessableEntity)) {
		if s, msg, _ := httpStatus(err); s == http.StatusNotFound || strings.Contains(msg, "Reference does not exist") {
			return nil
		}
	}
	return h.fail(ctx, fmt.Sprintf("delete branch %q", branch), 0, err)
}
