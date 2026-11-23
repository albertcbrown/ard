package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type reqRec struct {
	Method, Path string
	Query        url.Values
	Body         string
	Auth         string
}

// stubAPI routes "METHOD /path" to handlers and records every request.
type stubAPI struct {
	*httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	reqs   []reqRec
}

func newStubAPI(t *testing.T, routes map[string]http.HandlerFunc) *stubAPI {
	s := &stubAPI{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, reqRec{r.Method, r.URL.Path, r.URL.Query(), string(b), r.Header.Get("Authorization")})
		s.mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		h, ok := s.routes[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, `{"message":"no route `+r.Method+" "+r.URL.Path+`"}`, http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		h(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *stubAPI) requests() []reqRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]reqRec(nil), s.reqs...)
}

func respond(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); io.WriteString(w, body) }
}

func newTestHTTP(t *testing.T, s *stubAPI, mod func(*Config)) *HTTP {
	t.Helper()
	cfg := Config{Repo: Repo{"o", "r"}, Auth: NewTokenAuth("tok"), BaseURL: s.URL, MaxWait: time.Second,
		sleep: func(ctx context.Context, d time.Duration) error { return ctx.Err() }}
	if mod != nil {
		mod(&cfg)
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{Auth: NewTokenAuth("x")}); err == nil {
		t.Error("missing repo: want error")
	}
	if _, err := New(Config{Repo: Repo{"o", "r"}}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("missing auth err = %v", err)
	}
}

func TestGetIssueMapsFields(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"GET /repos/o/r/issues/7": respond(200, fixture(t, "issue.json")),
		"GET /repos/o/r/issues/9": respond(200, fixture(t, "pr_issue.json")),
		"GET /repos/o/r/issues/8": respond(404, `{"message":"Not Found"}`),
	})
	h := newTestHTTP(t, s, nil)
	got, err := h.GetIssue(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.Number != 7 || got.NodeID != "I_kwDOABC" || got.State != StateClosed || got.StateReason != ReasonNotPlanned ||
		got.Author != "alice" || got.IsPR || got.ClosedAt == nil || got.ClosedAt.Year() != 2026 ||
		strings.Join(got.Labels, ",") != "ard:pending,type:feature" || !strings.Contains(got.Body, "ignore previous") {
		t.Errorf("got %+v", got)
	}
	pr, _ := h.GetIssue(context.Background(), 9)
	if !pr.IsPR || pr.ClosedAt != nil {
		t.Errorf("pr view = %+v", pr)
	}
	if _, err := h.GetIssue(context.Background(), 8); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if auth := s.requests()[0].Auth; auth != "Bearer tok" {
		t.Errorf("Authorization = %q", auth)
	}
}

func TestListIssuesPaginatesFiltersPRsAndSendsParams(t *testing.T) {
	var base string
	s := newStubAPI(t, nil)
	s.routes = map[string]http.HandlerFunc{
		"GET /repos/o/r/issues": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"number":3,"state":"open"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/issues?page=2>; rel="next"`, base))
			fmt.Fprint(w, `[{"number":1,"state":"open"},{"number":2,"state":"open","pull_request":{"url":"x"}}]`)
		},
	}
	base = s.URL
	h := newTestHTTP(t, s, nil)
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := h.ListIssues(context.Background(), ListIssuesOpts{State: StateAll, Labels: []string{"a", "b"}, Since: since})
	if err != nil {
		t.Fatal(err)
	}
	if nums := fmt.Sprint(issueNums(got)); nums != "[1 3]" {
		t.Errorf("numbers = %s, want [1 3] (PR filtered)", nums)
	}
	q := s.requests()[0].Query
	if q.Get("labels") != "a,b" || q.Get("state") != "all" || q.Get("since") != "2026-01-01T00:00:00Z" {
		t.Errorf("query = %v", q)
	}
	withPR, _ := h.ListIssues(context.Background(), ListIssuesOpts{IncludePRs: true})
	if len(withPR) != 3 {
		t.Errorf("IncludePRs returned %d", len(withPR))
	}
	capped, _ := h.ListIssues(context.Background(), ListIssuesOpts{Max: 1})
	if len(capped) != 1 {
		t.Errorf("Max=1 returned %d", len(capped))
	}
	if s.requests()[len(s.requests())-1].Query.Get("state") != "open" {
		t.Error("default state is not open")
	}
}

func issueNums(is []Issue) []int {
	var out []int
	for _, i := range is {
		out = append(out, i.Number)
	}
	return out
}

func TestSearchIssues(t *testing.T) {
	var base string
	s := newStubAPI(t, nil)
	s.routes = map[string]http.HandlerFunc{
		"GET /search/issues": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"total_count":3,"items":[{"number":3}]}`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/search/issues?page=2>; rel="next"`, base))
			fmt.Fprint(w, `{"total_count":3,"items":[{"number":1},{"number":2}]}`)
		},
	}
	base = s.URL
	h := newTestHTTP(t, s, nil)
	got, err := h.SearchIssues(context.Background(), `is:open label:"x y"`, SearchOpts{Sort: "updated", Order: "asc", Max: 3})
	if err != nil || len(got) != 3 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	q := s.requests()[0].Query
	if q.Get("q") != `repo:o/r is:open label:"x y"` || q.Get("sort") != "updated" || q.Get("order") != "asc" {
		t.Errorf("query = %v", q)
	}
	n := len(s.requests())
	got, _ = h.SearchIssues(context.Background(), "is:open", SearchOpts{Max: 2})
	if len(got) != 2 || len(s.requests()) != n+1 {
		t.Errorf("Max=2: got %d results from %d requests, want stop after first page", len(got), len(s.requests())-n)
	}
}

func TestWritesRequestShapes(t *testing.T) {
	ok := respond(200, `{"number":7}`)
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"PATCH /repos/o/r/issues/7":            ok,
		"POST /repos/o/r/issues":               respond(201, `{"number":8,"title":"t"}`),
		"POST /repos/o/r/issues/7/labels":      respond(200, `[]`),
		"POST /repos/o/r/issues/7/comments":    respond(201, `{"id":5,"body":"hi","user":{"login":"ard[bot]"}}`),
		"POST /repos/o/r/pulls":                respond(201, `{"number":9,"draft":true,"head":{"ref":"b","sha":"s"},"base":{"ref":"main"}}`),
		"POST /repos/o/r/pulls/9/reviews":      respond(200, `{"id":77}`),
		"PATCH /repos/o/r/pulls/9":             respond(200, `{"number":9}`),
		"PUT /repos/o/r/pulls/9/update-branch": respond(202, `{"message":"Updating pull request branch."}`),
	})
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	closed, np := StateClosed, ReasonNotPlanned
	if err := h.UpdateIssue(ctx, 7, IssueUpdate{State: &closed, StateReason: &np}); err != nil {
		t.Fatal(err)
	}
	is, err := h.CreateIssue(ctx, NewIssue{Title: "t", Body: "b", Labels: []string{"l1"}})
	if err != nil || is.Number != 8 {
		t.Fatalf("create issue: %+v, %v", is, err)
	}
	if err := h.AddLabels(ctx, 7, "a", "b"); err != nil {
		t.Fatal(err)
	}
	c, err := h.CreateComment(ctx, 7, "hi")
	if err != nil || c.ID != 5 || c.Author != "ard[bot]" {
		t.Fatalf("comment: %+v, %v", c, err)
	}
	pr, err := h.CreatePR(ctx, NewPR{Title: "t", Head: "b", Base: "main", Draft: true})
	if err != nil || pr.Number != 9 || !pr.Draft || pr.HeadSHA != "s" || pr.BaseRef != "main" {
		t.Fatalf("pr: %+v, %v", pr, err)
	}
	if rid, err := h.CreateReview(ctx, 9, ReviewInput{CommitSHA: "s", Body: "ok", Comments: []InlineComment{{Path: "a.go", Line: 3, Body: "x"}}}); err != nil || rid != 77 {
		t.Fatal(rid, err)
	}
	title := "new"
	if err := h.UpdatePR(ctx, 9, PRUpdate{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateBranch(ctx, 9, "s"); err != nil {
		t.Errorf("update branch 202 err = %v, want nil", err)
	}

	body := func(i int) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s.requests()[i].Body), &m); err != nil {
			t.Fatalf("request %d body: %v", i, err)
		}
		return m
	}
	if m := body(0); m["state"] != "closed" || m["state_reason"] != "not_planned" {
		t.Errorf("update issue body = %v", m)
	}
	if m := body(1); m["title"] != "t" || fmt.Sprint(m["labels"]) != "[l1]" {
		t.Errorf("create issue body = %v", m)
	}
	if got := strings.TrimSpace(s.requests()[2].Body); got != `["a","b"]` {
		t.Errorf("add labels body = %s", got)
	}
	if m := body(4); m["draft"] != true || m["head"] != "b" || m["base"] != "main" {
		t.Errorf("create PR body = %v", m)
	}
	if m := body(5); m["event"] != "COMMENT" || m["commit_id"] != "s" {
		t.Errorf("review body = %v", m)
	} else if cs := m["comments"].([]any); cs[0].(map[string]any)["side"] != "RIGHT" || cs[0].(map[string]any)["line"] != float64(3) {
		t.Errorf("review comments = %v", cs)
	}
	if m := body(7); m["expected_head_sha"] != "s" {
		t.Errorf("update branch body = %v", m)
	}
}

func TestLabelOps(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"DELETE /repos/o/r/issues/7/labels/gone": respond(404, `{"message":"Label does not exist"}`),
		"DELETE /repos/o/r/issues/7/labels/boom": respond(500, `{"message":"boom"}`),
		"POST /repos/o/r/labels":                 respond(422, `{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}]}`),
		"PATCH /repos/o/r/labels/old":            respond(200, `{"name":"new"}`),
		"GET /repos/o/r/labels":                  respond(200, `[{"name":"a","color":"fff000","description":"d"}]`),
	})
	h := newTestHTTP(t, s, func(c *Config) { c.MaxWait = time.Millisecond })
	ctx := context.Background()
	if err := h.RemoveLabel(ctx, 7, "gone"); err != nil {
		t.Errorf("404 remove err = %v, want nil", err)
	}
	if err := h.CreateLabel(ctx, Label{Name: "x", Color: "ffffff"}); !errors.Is(err, ErrConflict) {
		t.Errorf("create existing err = %v, want ErrConflict", err)
	}
	if err := h.UpdateLabel(ctx, "old", Label{Name: "new", Color: "000000"}); err != nil {
		t.Fatal(err)
	}
	ls, err := h.ListLabels(ctx)
	if err != nil || len(ls) != 1 || ls[0] != (Label{"a", "fff000", "d"}) {
		t.Errorf("labels = %+v, %v", ls, err)
	}
}

func TestListTimelineMapping(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{"GET /repos/o/r/issues/7/timeline": respond(200, fixture(t, "timeline.json"))})
	h := newTestHTTP(t, s, nil)
	tl, err := h.ListTimeline(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(tl) != 4 {
		t.Fatalf("len = %d", len(tl))
	}
	if tl[0].Event != "labeled" || tl[0].Label != "ard:pending" || tl[0].Actor != "ard[bot]" {
		t.Errorf("labeled = %+v", tl[0])
	}
	if tl[1].Event != "commented" || tl[1].Body != "hello" || tl[1].Actor != "alice" {
		t.Errorf("commented = %+v", tl[1])
	}
	if tl[2].Source == nil || *tl[2].Source != (IssueRef{Number: 9, IsPR: true, State: StateOpen}) {
		t.Errorf("cross-referenced = %+v", tl[2])
	}
	if tl[3].Actor != "" || tl[3].Event != "closed" {
		t.Errorf("closed = %+v", tl[3])
	}
	if !tl[0].CreatedAt.Before(tl[3].CreatedAt) {
		t.Error("not oldest first")
	}
}

func TestGetPermission(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"GET /repos/o/r/collaborators/alice/permission": respond(200, `{"permission":"write","role_name":"maintain"}`),
		"GET /repos/o/r/collaborators/ghost/permission": respond(404, `{"message":"Not Found"}`),
		"GET /repos/o/r/collaborators/bad/permission":   respond(401, `{"message":"Bad credentials"}`),
	})
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	if p, err := h.GetPermission(ctx, "alice"); err != nil || p != (Permission{"write", "maintain"}) {
		t.Errorf("alice = %+v, %v", p, err)
	}
	if p, err := h.GetPermission(ctx, "ghost"); err != nil || p.Level != "none" {
		t.Errorf("ghost = %+v, %v", p, err)
	}
	if _, err := h.GetPermission(ctx, "bad"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bad err = %v", err)
	}
}

func TestMergePR(t *testing.T) {
	status := 200
	s := newStubAPI(t, nil)
	s.routes = map[string]http.HandlerFunc{
		"PUT /repos/o/r/pulls/9/merge": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			if status == 200 {
				fmt.Fprint(w, `{"sha":"merged123","merged":true,"message":"ok"}`)
				return
			}
			fmt.Fprint(w, `{"message":"nope"}`)
		},
	}
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	for _, tc := range []struct {
		status int
		want   error
	}{{409, ErrConflict}, {405, ErrNotMergeable}} {
		status = tc.status
		if _, err := h.MergePR(ctx, 9, MergeInput{SHA: "abc"}); !errors.Is(err, tc.want) {
			t.Errorf("status %d err = %v, want %v", tc.status, err, tc.want)
		}
	}
	status = 200
	res, err := h.MergePR(ctx, 9, MergeInput{SHA: "abc", Title: "T", Message: "M"})
	if err != nil || res.SHA != "merged123" {
		t.Fatalf("merge = %+v, %v", res, err)
	}
	var m map[string]any
	reqs := s.requests()
	if err := json.Unmarshal([]byte(reqs[len(reqs)-1].Body), &m); err != nil {
		t.Fatal(err)
	}
	if m["merge_method"] != "squash" || m["sha"] != "abc" || m["commit_title"] != "T" || m["commit_message"] != "M" {
		t.Errorf("merge body = %v", m)
	}
	if _, err := h.MergePR(ctx, 9, MergeInput{}); err == nil {
		t.Error("merge without SHA: want error")
	}
}

func TestUpdateBranchConflictAndDeleteBranch(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"PUT /repos/o/r/pulls/9/update-branch":  respond(422, `{"message":"expected head sha didn't match"}`),
		"DELETE /repos/o/r/git/refs/heads/gone": respond(422, `{"message":"Reference does not exist"}`),
		"DELETE /repos/o/r/git/refs/heads/miss": respond(404, `{"message":"Not Found"}`),
		"DELETE /repos/o/r/git/refs/heads/ok":   respond(204, ``),
		"DELETE /repos/o/r/git/refs/heads/prot": respond(422, `{"message":"Cannot delete protected branch"}`),
	})
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	if err := h.UpdateBranch(ctx, 9, "s"); !errors.Is(err, ErrConflict) {
		t.Errorf("update branch 422 err = %v", err)
	}
	for _, b := range []string{"gone", "miss", "ok"} {
		if err := h.DeleteBranch(ctx, b); err != nil {
			t.Errorf("delete %s err = %v", b, err)
		}
	}
	if err := h.DeleteBranch(ctx, "prot"); err == nil {
		t.Error("protected branch delete: want error")
	}
}

func TestPinIssueAndMarkReadyGraphQL(t *testing.T) {
	gql := respond(200, `{"data":{"pinIssue":{"issue":{"id":"I_kwDOABC"}}}}`)
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"GET /repos/o/r/issues/7": respond(200, fixture(t, "issue.json")),
		"GET /repos/o/r/pulls/9":  respond(200, `{"number":9,"node_id":"PR_node","draft":true}`),
		"POST /graphql":           func(w http.ResponseWriter, r *http.Request) { gql(w, r) },
	})
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	if err := h.PinIssue(ctx, 7); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Query     string
		Variables map[string]string
	}
	reqs := s.requests()
	if err := json.Unmarshal([]byte(reqs[len(reqs)-1].Body), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["id"] != "I_kwDOABC" || !strings.Contains(sent.Query, "pinIssue") {
		t.Errorf("graphql request = %+v", sent)
	}
	if err := h.MarkReady(ctx, 9); err != nil {
		t.Fatal(err)
	}
	reqs = s.requests()
	if err := json.Unmarshal([]byte(reqs[len(reqs)-1].Body), &sent); err != nil || sent.Variables["id"] != "PR_node" ||
		!strings.Contains(sent.Query, "markPullRequestReadyForReview") {
		t.Errorf("mark ready request = %+v, %v", sent, err)
	}
	gql = respond(200, `{"errors":[{"message":"Could not resolve"},{"message":"second"}]}`)
	err := h.PinIssue(ctx, 7)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 200 || !strings.Contains(he.Message, "Could not resolve; second") {
		t.Errorf("graphql error = %v", err)
	}
}

func TestReadOnlyConfigRefusesWrites(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"GET /repos/o/r/issues/7": respond(200, fixture(t, "issue.json")),
	})
	h := newTestHTTP(t, s, func(c *Config) { c.ReadOnly = true })
	ctx := context.Background()
	if _, err := h.GetIssue(ctx, 7); err != nil {
		t.Fatal(err)
	}
	before := len(s.requests())
	closed := StateClosed
	checks := map[string]error{
		"UpdateIssue":   h.UpdateIssue(ctx, 7, IssueUpdate{State: &closed}),
		"AddLabels":     h.AddLabels(ctx, 7, "x"),
		"RemoveLabel":   h.RemoveLabel(ctx, 7, "x"),
		"PinIssue":      h.PinIssue(ctx, 7),
		"DeleteBranch":  h.DeleteBranch(ctx, "b"),
		"UpdateBranch":  h.UpdateBranch(ctx, 9, "s"),
		"CreateComment": func() error { _, err := h.CreateComment(ctx, 7, "x"); return err }(),
		"MergePR":       func() error { _, err := h.MergePR(ctx, 9, MergeInput{SHA: "s"}); return err }(),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s err = %v, want ErrReadOnly", name, err)
		}
	}
	// PinIssue does one GET (node id) before the refused mutation.
	if got := len(s.requests()) - before; got != 1 {
		t.Errorf("server saw %d requests after read-only writes, want 1 (the node-id GET)", got)
	}
	for _, r := range s.requests()[before:] {
		if r.Method != "GET" {
			t.Errorf("non-GET reached the server: %+v", r)
		}
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		name   string
		status int
		hdr    map[string]string
		body   string
		check  func(error) bool
	}{
		{"401", 401, nil, `{"message":"Bad credentials"}`, func(e error) bool { return errors.Is(e, ErrUnauthorized) }},
		{"403", 403, nil, `{"message":"Resource not accessible by integration"}`, func(e error) bool { return errors.Is(e, ErrForbidden) }},
		{"404", 404, nil, `{"message":"Not Found"}`, func(e error) bool { return errors.Is(e, ErrNotFound) }},
		{"422", 422, nil, `{"message":"Validation Failed"}`, func(e error) bool {
			var he *HTTPError
			return errors.As(e, &he) && he.Status == 422 && !IsTransient(e)
		}},
		{"rate limit 403", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(time.Now().Add(time.Hour).Unix())},
			`{"message":"API rate limit exceeded"}`, func(e error) bool {
				var rl *RateLimitError
				return errors.As(e, &rl) && IsTransient(e)
			}},
		{"500", 500, nil, `{"message":"oops"}`, func(e error) bool {
			var he *HTTPError
			return errors.As(e, &he) && he.Status == 500 && IsTransient(e)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStubAPI(t, map[string]http.HandlerFunc{
				"GET /repos/o/r/issues/1": func(w http.ResponseWriter, r *http.Request) {
					for k, v := range tc.hdr {
						w.Header().Set(k, v)
					}
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
				},
			})
			h := newTestHTTP(t, s, func(c *Config) { c.MaxWait = time.Millisecond })
			_, err := h.GetIssue(context.Background(), 1)
			if err == nil || !tc.check(err) {
				t.Errorf("err = %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "tok") && strings.Contains(err.Error(), "Bearer") {
				t.Errorf("error leaks credentials: %v", err)
			}
		})
	}
}

func TestIdentity(t *testing.T) {
	t.Run("token", func(t *testing.T) {
		s := newStubAPI(t, map[string]http.HandlerFunc{"GET /user": respond(200, `{"login":"alice","id":5}`)})
		h := newTestHTTP(t, s, nil)
		for i := 0; i < 2; i++ {
			id, err := h.Identity(context.Background())
			if err != nil || id != (Identity{"alice", 5, AuthToken}) {
				t.Fatalf("id = %+v, %v", id, err)
			}
		}
		if n := len(s.requests()); n != 1 {
			t.Errorf("requests = %d, want 1 (cached)", n)
		}
	})
	t.Run("app", func(t *testing.T) {
		s := newStubAPI(t, map[string]http.HandlerFunc{
			"POST /app/installations/34/access_tokens": respond(201, fmt.Sprintf(`{"token":"inst","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))),
			"GET /app":                respond(200, `{"slug":"ard-app"}`),
			"GET /users/ard-app[bot]": respond(200, `{"login":"ard-app[bot]","id":4242}`),
		})
		auth := &AppAuth{AppID: 12, InstallationID: 34, PrivateKey: []byte(testKeyPEM(t))}
		h := newTestHTTP(t, s, func(c *Config) { c.Auth = auth })
		id, err := h.Identity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if id != (Identity{"ard-app[bot]", 4242, AuthApp}) {
			t.Errorf("id = %+v", id)
		}
		if got := id.NoreplyEmail(); got != "4242+ard-app[bot]@users.noreply.github.com" {
			t.Errorf("email = %s", got)
		}
		for _, r := range s.requests() {
			switch r.Path {
			case "/app":
				if strings.Count(r.Auth, ".") != 2 {
					t.Errorf("/app not JWT-authenticated: %q", r.Auth)
				}
			case "/users/ard-app[bot]":
				if r.Auth != "token inst" {
					t.Errorf("/users auth = %q, want installation token", r.Auth)
				}
			}
		}
	})
}

func TestReviewAndPRListing(t *testing.T) {
	s := newStubAPI(t, map[string]http.HandlerFunc{
		"GET /repos/o/r/pulls":             respond(200, `[{"number":9,"state":"open","mergeable_state":"clean","head":{"ref":"feat","sha":"s"},"base":{"ref":"main"}}]`),
		"GET /repos/o/r/pulls/9":           respond(200, `{"number":9,"state":"closed","merged":true,"mergeable":null,"head":{"ref":"feat","sha":"s"}}`),
		"GET /repos/o/r/pulls/9/files":     respond(200, `[{"filename":"b.go","previous_filename":"a.go","status":"renamed","additions":2,"deletions":1}]`),
		"GET /repos/o/r/pulls/9/comments":  respond(200, `[{"id":3,"user":{"login":"r"},"path":"b.go","original_line":4,"body":"x","commit_id":"s"}]`),
		"GET /repos/o/r/issues/9/comments": respond(200, `[{"id":1,"body":"c","user":{"login":"u"}}]`),
	})
	h := newTestHTTP(t, s, nil)
	ctx := context.Background()
	prs, err := h.ListPRs(ctx, ListPRsOpts{Head: "feat", Base: "main"})
	if err != nil || len(prs) != 1 || prs[0].HeadRef != "feat" || prs[0].MergeableState != "clean" {
		t.Fatalf("prs = %+v, %v", prs, err)
	}
	if q := s.requests()[0].Query; q.Get("head") != "o:feat" || q.Get("base") != "main" || q.Get("state") != "open" {
		t.Errorf("query = %v", q)
	}
	pr, _ := h.GetPR(ctx, 9)
	if !pr.Merged || pr.Mergeable != nil || pr.State != StateClosed {
		t.Errorf("pr = %+v", pr)
	}
	fs, _ := h.ListPRFiles(ctx, 9)
	if len(fs) != 1 || fs[0] != (PRFile{"b.go", "a.go", "renamed", 2, 1}) {
		t.Errorf("files = %+v", fs)
	}
	rc, _ := h.ListReviewComments(ctx, 9)
	if len(rc) != 1 || rc[0].Line != 4 || rc[0].Author != "r" {
		t.Errorf("review comments = %+v", rc)
	}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cs, _ := h.ListComments(ctx, 9, since)
	if len(cs) != 1 || cs[0].Author != "u" {
		t.Errorf("comments = %+v", cs)
	}
	if q := s.requests()[len(s.requests())-1].Query; q.Get("since") != "2026-01-01T00:00:00Z" {
		t.Errorf("comments query = %v", q)
	}
}

func TestListReviewsPaginates(t *testing.T) {
	var base string
	s := newStubAPI(t, nil)
	s.routes = map[string]http.HandlerFunc{
		"GET /repos/o/r/pulls/9/reviews": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `[{"id":2,"user":{"login":"b"},"body":"two","state":"CHANGES_REQUESTED","commit_id":"c2"}]`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/pulls/9/reviews?page=2>; rel="next"`, base))
			fmt.Fprint(w, `[{"id":1,"user":{"login":"a"},"body":"one","state":"COMMENTED","commit_id":"c1","submitted_at":"2026-01-02T03:04:05Z"}]`)
		},
	}
	base = s.URL
	h := newTestHTTP(t, s, nil)
	got, err := h.ListReviews(context.Background(), 9)
	if err != nil || len(got) != 2 {
		t.Fatalf("reviews = %+v, %v", got, err)
	}
	if g := got[0]; g.ID != 1 || g.Author != "a" || g.Body != "one" || g.State != "COMMENTED" || g.CommitSHA != "c1" || g.SubmittedAt.IsZero() {
		t.Errorf("first = %+v", g)
	}
	if g := got[1]; g.ID != 2 || g.State != "CHANGES_REQUESTED" || g.CommitSHA != "c2" {
		t.Errorf("second = %+v", g)
	}
}

// redirect answers with a 3xx whose Location is path on the stub's own host.
func redirect(t *testing.T, code int, path string) http.HandlerFunc {
	body := fixture(t, "moved.json")
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+path+"?token=x")
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func TestRedirects(t *testing.T) {
	ctx := context.Background()
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { otherHits.Add(1) }))
	t.Cleanup(other.Close)

	notFound := func(e error) bool { return errors.Is(e, ErrNotFound) && !errors.Is(e, ErrRepoMoved) }
	httpErr := func(status int) func(error) bool {
		return func(e error) bool {
			var he *HTTPError
			return errors.As(e, &he) && he.Status == status && !errors.Is(e, ErrNotFound) && !IsTransient(e)
		}
	}
	moved := func(to Repo, contains ...string) func(error) bool {
		return func(e error) bool {
			var rm *RepoMovedError
			if !errors.As(e, &rm) || !errors.Is(e, ErrRepoMoved) || errors.Is(e, ErrNotFound) || IsTransient(e) {
				return false
			}
			if rm.Repo != (Repo{"o", "r"}) || rm.To != to {
				return false
			}
			for _, c := range contains {
				if !strings.Contains(e.Error(), c) {
					return false
				}
			}
			return true
		}
	}
	repoOK := respond(200, fixture(t, "repo.json"))
	tests := []struct {
		name      string
		routes    map[string]http.HandlerFunc
		call      func(h *HTTP) error
		check     func(error) bool
		forbidden []string // paths no request may reach
	}{
		{"GetIssue transferred (repositories id)", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repositories/42/issues/3"),
			"GET /repos/o/r":          repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, notFound, []string{"/repositories/42/issues/3"}},
		{"GetIssue transferred (repos shape)", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repos/x/y/issues/3"),
			"GET /repos/o/r":          repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, notFound, []string{"/repos/x/y/issues/3"}},
		{"ListComments transferred", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3/comments": redirect(t, 301, "/repositories/42/issues/3/comments"),
			"GET /repos/o/r":                   repoOK,
		}, func(h *HTTP) error { _, err := h.ListComments(ctx, 3, time.Time{}); return err }, notFound, []string{"/repositories/42/issues/3/comments"}},
		{"ListTimeline transferred", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3/timeline": redirect(t, 301, "/repositories/42/issues/3/timeline"),
			"GET /repos/o/r":                   repoOK,
		}, func(h *HTTP) error { _, err := h.ListTimeline(ctx, 3); return err }, notFound, []string{"/repositories/42/issues/3/timeline"}},
		{"AddLabels transferred via 307", map[string]http.HandlerFunc{
			"POST /repos/o/r/issues/3/labels": redirect(t, 307, "/repositories/42/issues/3/labels"),
			"GET /repos/o/r":                  repoOK,
		}, func(h *HTTP) error { return h.AddLabels(ctx, 3, "x") }, notFound, []string{"/repositories/42/issues/3/labels"}},
		{"CreateComment transferred via 301", map[string]http.HandlerFunc{
			"POST /repos/o/r/issues/3/comments": redirect(t, 301, "/repositories/42/issues/3/comments"),
			"GET /repos/o/r":                    repoOK,
		}, func(h *HTTP) error { _, err := h.CreateComment(ctx, 3, "x"); return err }, notFound, []string{"/repositories/42/issues/3/comments"}},
		{"RemoveLabel transferred", map[string]http.HandlerFunc{
			"DELETE /repos/o/r/issues/3/labels/x": redirect(t, 301, "/repositories/42/issues/3/labels/x"),
			"GET /repos/o/r":                      repoOK,
		}, func(h *HTTP) error { return h.RemoveLabel(ctx, 3, "x") }, notFound, []string{"/repositories/42/issues/3/labels/x"}},
		{"GetIssue redirect to the same repo id", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/9": redirect(t, 301, "/repositories/7/issues/9"),
			"GET /repos/o/r":          repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 9); return err }, httpErr(301), []string{"/repositories/7/issues/9"}},
		{"missing Location", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/9": respond(301, fixture(t, "moved.json")),
			"GET /repos/o/r":          repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 9); return err }, httpErr(301), nil},
		{"cross-host Location", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/9": func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", other.URL+"/repositories/42/issues/9")
				w.WriteHeader(301)
			},
			"GET /repos/o/r": repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 9); return err }, httpErr(301), nil},
		{"probe 404", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repositories/42/issues/3"),
			"GET /repos/o/r":          respond(404, `{"message":"Not Found"}`),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, func(e error) bool {
			var he *HTTPError
			return !errors.Is(e, ErrNotFound) && errors.As(e, &he)
		}, []string{"/repositories/42/issues/3"}},
		{"probe without id", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repositories/42/issues/3"),
			"GET /repos/o/r":          respond(200, `{"full_name":"o/r"}`),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, httpErr(301), []string{"/repositories/42/issues/3"}},
		{"invalid repo name in Location", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repos/x/%20/issues/3"),
			"GET /repos/o/r":          repoOK,
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, httpErr(301), nil},
		{"probe 500", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/3": redirect(t, 301, "/repositories/42/issues/3"),
			"GET /repos/o/r":          respond(500, `{"message":"oops"}`),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 3); return err }, func(e error) bool {
			return IsTransient(e) && !errors.Is(e, ErrNotFound)
		}, []string{"/repositories/42/issues/3"}},
		{"renamed, name resolved", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/7": redirect(t, 301, "/repositories/7/issues/7"),
			"GET /repos/o/r":          redirect(t, 301, "/repositories/7"),
			"GET /repositories/7":     respond(200, fixture(t, "repo_renamed.json")),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 7); return err }, moved(Repo{"o2", "r2"}, "o/r", "o2/r2"), []string{"/repositories/7/issues/7"}},
		{"renamed, resolve fails", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/7": redirect(t, 301, "/repositories/7/issues/7"),
			"GET /repos/o/r":          redirect(t, 301, "/repositories/7"),
			"GET /repositories/7":     respond(404, `{"message":"Not Found"}`),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 7); return err }, moved(Repo{}, "/repositories/7"), []string{"/repositories/7/issues/7"}},
		{"ListLabels on renamed repo", map[string]http.HandlerFunc{
			"GET /repos/o/r/labels": redirect(t, 301, "/repositories/7/labels"),
			"GET /repos/o/r":        redirect(t, 301, "/repositories/7"),
			"GET /repositories/7":   respond(200, fixture(t, "repo_renamed.json")),
		}, func(h *HTTP) error { _, err := h.ListLabels(ctx); return err }, moved(Repo{"o2", "r2"}), []string{"/repositories/7/labels"}},
		{"ListLabels redirect, repo 200", map[string]http.HandlerFunc{
			"GET /repos/o/r/labels": redirect(t, 301, "/repositories/7/labels"),
			"GET /repos/o/r":        repoOK,
		}, func(h *HTTP) error { _, err := h.ListLabels(ctx); return err }, httpErr(301), []string{"/repositories/7/labels"}},
		{"GetPermission on renamed repo", map[string]http.HandlerFunc{
			"GET /repos/o/r/collaborators/al/permission": redirect(t, 301, "/repositories/7/collaborators/al/permission"),
			"GET /repos/o/r":      redirect(t, 301, "/repositories/7"),
			"GET /repositories/7": respond(404, `{"message":"Not Found"}`),
		}, func(h *HTTP) error { _, err := h.GetPermission(ctx, "al"); return err }, moved(Repo{}), []string{"/repositories/7/collaborators/al/permission"}},
		{"CreateIssue on renamed repo (307)", map[string]http.HandlerFunc{
			"POST /repos/o/r/issues": redirect(t, 307, "/repositories/7/issues"),
			"GET /repos/o/r":         redirect(t, 301, "/repositories/7"),
			"GET /repositories/7":    respond(404, `{"message":"Not Found"}`),
		}, func(h *HTTP) error { _, err := h.CreateIssue(ctx, NewIssue{Title: "t"}); return err }, moved(Repo{}), []string{"/repositories/7/issues"}},
		{"Identity 301", map[string]http.HandlerFunc{
			"GET /user": redirect(t, 301, "/elsewhere"),
		}, func(h *HTTP) error { _, err := h.Identity(ctx); return err }, httpErr(301), []string{"/elsewhere"}},
		{"PinIssue GraphQL 307", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/7": respond(200, fixture(t, "issue.json")),
			"POST /graphql":           redirect(t, 307, "/elsewhere/graphql"),
		}, func(h *HTTP) error { return h.PinIssue(ctx, 7) }, httpErr(307), []string{"/elsewhere/graphql"}},
		{"deleted issue 410", map[string]http.HandlerFunc{
			"GET /repos/o/r/issues/5": respond(410, `{"message":"This issue was deleted"}`),
		}, func(h *HTTP) error { _, err := h.GetIssue(ctx, 5); return err }, notFound, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			otherHits.Store(0)
			s := newStubAPI(t, tc.routes)
			h := newTestHTTP(t, s, nil)
			err := tc.call(h)
			if err == nil || !tc.check(err) {
				t.Fatalf("err = %v (%T)", err, err)
			}
			for _, r := range s.requests() {
				for _, f := range tc.forbidden {
					if r.Path == f {
						t.Errorf("redirect target was requested: %s %s", r.Method, r.Path)
					}
				}
				if r.Path == "/elsewhere" || r.Path == "/teapot" {
					t.Errorf("unexpected request %s %s", r.Method, r.Path)
				}
			}
			if n := otherHits.Load(); n != 0 {
				t.Errorf("other host got %d requests", n)
			}
			if msg := err.Error(); strings.Contains(msg, "Bearer") || strings.Contains(msg, "tok") && strings.Contains(msg, "Authorization") || strings.Contains(msg, "token=x") {
				t.Errorf("error leaks credentials or query: %v", err)
			}
		})
	}
}

func TestRepoMovedErrorIsNotTransient(t *testing.T) {
	e := fmt.Errorf("wrapped: %w", &RepoMovedError{Repo: Repo{"o", "r"}, Location: "/repositories/7"})
	if IsTransient(e) || !errors.Is(e, ErrRepoMoved) || errors.Is(e, ErrNotFound) {
		t.Errorf("err = %v", e)
	}
}

func TestRequiredChecks(t *testing.T) {
	ctx := context.Background()
	const rules = "GET /repos/o/r/rules/branches/main"
	const upgrade = `{"message":"Upgrade to GitHub Pro or make this repository public to enable this feature.","documentation_url":"https://docs.github.com/rest/repos/rules#get-rules-for-a-branch","status":"403"}`
	var base string
	paged := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[{"type":"required_status_checks","ruleset_id":2,"parameters":{"required_status_checks":[{"context":"build","integration_id":15368},{"context":"lint"}]}}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/rules/branches/main?page=2>; rel="next"`, base))
		fmt.Fprint(w, `[{"type":"deletion","ruleset_id":1},{"type":"required_status_checks","ruleset_id":1,"parameters":{"required_status_checks":[{"context":"test"},{"context":"build","integration_id":15368}]}}]`)
	}
	tests := []struct {
		name    string
		h       http.HandlerFunc
		want    RequiredChecksResult
		wantErr func(error) bool // nil: no error
	}{
		{"paginated, deduped, sorted, nil integration id", paged,
			RequiredChecksResult{Available: true, Checks: []RequiredCheck{{"build", 15368}, {"lint", 0}, {"test", 0}}}, nil},
		{"empty list", respond(200, `[]`), RequiredChecksResult{Available: true}, nil},
		{"no checks rule", respond(200, `[{"type":"deletion","ruleset_id":1}]`), RequiredChecksResult{Available: true}, nil},
		{"403 upgrade is unavailable", respond(403, upgrade), RequiredChecksResult{}, nil},
		{"404 is unavailable", respond(404, `{"message":"Not Found"}`), RequiredChecksResult{}, nil},
		{"permissions 403 is an error", respond(403, `{"message":"Resource not accessible by integration"}`), RequiredChecksResult{},
			func(e error) bool { return errors.Is(e, ErrForbidden) }},
		{"403 rate limit header is a transient error", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "4102444800")
			w.WriteHeader(403)
			fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
		}, RequiredChecksResult{}, IsTransient},
		{"403 secondary rate limit is a transient error", respond(403, `{"message":"You have exceeded a secondary rate limit. Please wait."}`), RequiredChecksResult{}, IsTransient},
		{"403 upgrade to Team is unavailable", respond(403, `{"message":"Upgrade to GitHub Team or make this repository public to enable this feature."}`), RequiredChecksResult{}, nil},
		{"404 on page 2 is an error", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/rules/branches/main?page=2>; rel="next"`, base))
			fmt.Fprint(w, `[{"type":"required_status_checks","ruleset_id":1,"parameters":{"required_status_checks":[{"context":"test"}]}}]`)
		}, RequiredChecksResult{}, func(e error) bool { return errors.Is(e, ErrNotFound) }},
		{"500 is a transient error", respond(500, `{"message":"boom"}`), RequiredChecksResult{}, IsTransient},
		{"unexpected status is an error", respond(418, `{"message":"teapot"}`), RequiredChecksResult{},
			func(e error) bool { var he *HTTPError; return errors.As(e, &he) && he.Status == 418 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStubAPI(t, map[string]http.HandlerFunc{rules: tt.h})
			base = s.URL
			h := newTestHTTP(t, s, nil)
			got, err := h.RequiredChecks(ctx, "main")
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestListCheckRunsPaginates(t *testing.T) {
	var base string
	s := newStubAPI(t, nil)
	s.routes = map[string]http.HandlerFunc{
		"GET /repos/o/r/commits/abc/check-runs": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("filter") != "latest" {
				t.Errorf("filter = %q", r.URL.Query().Get("filter"))
			}
			if r.URL.Query().Get("page") == "2" {
				fmt.Fprint(w, `{"total_count":2,"check_runs":[{"id":8,"name":"lint","status":"in_progress"}]}`)
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/commits/abc/check-runs?page=2>; rel="next"`, base))
			fmt.Fprint(w, `{"total_count":2,"check_runs":[{"id":7,"name":"build","status":"completed","conclusion":"success","completed_at":"2026-01-02T03:04:05Z","app":{"id":15368}}]}`)
		},
	}
	base = s.URL
	h := newTestHTTP(t, s, nil)
	got, err := h.ListCheckRuns(context.Background(), "abc")
	want := []CheckRun{{ID: 7, Name: "build", AppID: 15368, Status: "completed", Conclusion: "success", CompletedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}, {ID: 8, Name: "lint", Status: "in_progress"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, %v", got, err)
	}

	s2 := newStubAPI(t, map[string]http.HandlerFunc{"GET /repos/o/r/commits/abc/check-runs": respond(404, `{"message":"Not Found"}`)})
	if _, err := newTestHTTP(t, s2, nil).ListCheckRuns(context.Background(), "abc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
