package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/state"
)

const bot = "ard[bot]"

func statusIssue(author string) github.Issue {
	return github.Issue{Title: StatusTitle, Body: StatusMarker + "\nbody", Author: author}
}

func TestEnsureStatusIssue(t *testing.T) {
	ctx := context.Background()
	t.Run("found by search", func(t *testing.T) {
		f := fake.New()
		n := f.SeedIssue(statusIssue(bot))
		log, _ := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, nil, bot, log)
		if err != nil || got != n || len(f.Writes()) != 0 {
			t.Fatalf("got %d, %v, writes %v", got, err, f.Writes())
		}
	})
	t.Run("lookalikes are ignored", func(t *testing.T) {
		f := fake.New()
		noMarker := statusIssue(bot)
		noMarker.Body = "no marker"
		f.SeedIssue(noMarker)
		f.SeedIssue(statusIssue("stranger"))
		f.SeedIssue(github.Issue{Title: "other", Body: StatusMarker, Author: bot})
		log, _ := newLog()
		got, err := EnsureStatusIssue(ctx, f, f.As(bot), nil, bot, log)
		if err != nil {
			t.Fatal(err)
		}
		if got != 4 || !f.Pinned(got) {
			t.Errorf("want a new pinned issue, got #%d pinned=%v", got, f.Pinned(got))
		}
	})
	t.Run("two matches use the lowest and warn", func(t *testing.T) {
		f := fake.New()
		a := f.SeedIssue(statusIssue(bot))
		f.SeedIssue(statusIssue(bot))
		log, buf := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, nil, bot, log)
		if err != nil || got != a || buf.count("status.duplicate") != 1 {
			t.Fatalf("got %d, %v\n%s", got, err, buf.String())
		}
	})
	t.Run("none: created and pinned", func(t *testing.T) {
		f := fake.New()
		log, _ := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, nil, bot, log)
		if err != nil || got <= 0 || !f.Pinned(got) {
			t.Fatalf("got %d, %v", got, err)
		}
		is, _ := f.GetIssue(ctx, got)
		if is.Title != StatusTitle || len(is.Labels) != 0 {
			t.Errorf("issue = %+v", is)
		}
	})
	t.Run("restart reuses the created issue", func(t *testing.T) {
		f := fake.New()
		fx := f.As(bot)
		log, _ := newLog()
		a, err := EnsureStatusIssue(ctx, f, fx, nil, bot, log)
		if err != nil {
			t.Fatal(err)
		}
		b, err := EnsureStatusIssue(ctx, f, fx, nil, bot, log)
		if err != nil || a != b {
			t.Fatalf("got %d then %d, %v", a, b, err)
		}
		creates := 0
		for _, w := range f.Writes() {
			if w.Method == "CreateIssue" {
				creates++
			}
		}
		if creates != 1 {
			t.Errorf("CreateIssue called %d times, want 1", creates)
		}
	})
	t.Run("pin error is a warning", func(t *testing.T) {
		f := fake.New()
		f.FailNext("PinIssue", errors.New("3 pinned already"))
		log, buf := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, nil, bot, log)
		if err != nil || got <= 0 || buf.count("status.pin_failed") != 1 {
			t.Fatalf("got %d, %v\n%s", got, err, buf.String())
		}
	})
	t.Run("search error is returned", func(t *testing.T) {
		f := fake.New()
		f.FailNext("SearchIssues", errors.New("down"))
		log, _ := newLog()
		if _, err := EnsureStatusIssue(ctx, f, f, nil, bot, log); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("dry-run returns a synthetic number and writes nothing", func(t *testing.T) {
		f := fake.New()
		log, _ := newLog()
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: log})
		if err != nil {
			t.Fatal(err)
		}
		got, err := EnsureStatusIssue(ctx, f, d, nil, bot, log)
		if err != nil || got >= 0 || len(f.Writes()) != 0 {
			t.Fatalf("got %d, %v, writes %v", got, err, f.Writes())
		}
	})
}

// memCache is an in-memory StatusCache.
type memCache struct {
	m        map[string]string
	readErr  error
	writeErr error
	sets     int
}

func (c *memCache) Meta(_ context.Context, key string) (string, error) {
	if c.readErr != nil {
		return "", c.readErr
	}
	v, ok := c.m[key]
	if !ok {
		return "", fmt.Errorf("meta %q: %w", key, state.ErrNotFound)
	}
	return v, nil
}

func (c *memCache) SetMeta(_ context.Context, key, value string) error {
	if c.writeErr != nil {
		return c.writeErr
	}
	if c.m == nil {
		c.m = map[string]string{}
	}
	c.m[key] = value
	c.sets++
	return nil
}

// lagReader counts searches and can simulate search lag or a transfer redirect.
type lagReader struct {
	StatusReader
	lag       bool
	searches  int
	getNumber func(int) int
}

func (l *lagReader) SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error) {
	l.searches++
	if l.lag {
		return nil, nil
	}
	return l.StatusReader.SearchIssues(ctx, q, o)
}

func (l *lagReader) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	is, err := l.StatusReader.GetIssue(ctx, n)
	if err == nil && l.getNumber != nil {
		is.Number = l.getNumber(is.Number)
	}
	return is, err
}

func creates(f *fake.Fake) int {
	c := 0
	for _, w := range f.Writes() {
		if w.Method == "CreateIssue" {
			c++
		}
	}
	return c
}

func TestEnsureStatusIssueCache(t *testing.T) {
	ctx := context.Background()
	own := statusIssue(bot)
	closed := statusIssue(bot)
	closed.State = github.StateClosed
	noMarker := statusIssue(bot)
	noMarker.Body = "no marker"
	retitled := statusIssue(bot)
	retitled.Title = "renamed"
	tests := []struct {
		name       string
		seed       func(f *fake.Fake)
		cache      *memCache
		getNumber  func(int) int
		failGet    error
		wantNum    int // 0: a newly created issue
		wantSearch int
		wantCreate int
		wantCache  string
		wantSets   int
		wantLog    string
	}{
		{name: "cached open own issue adopted", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 1, wantCache: "1"},
		{name: "cached closed falls back to search", seed: func(f *fake.Fake) { f.SeedIssue(closed); f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 2, wantSearch: 1,
			wantCache: "2", wantSets: 1, wantLog: "reason=closed"},
		{name: "cached closed, none found: create", seed: func(f *fake.Fake) { f.SeedIssue(closed) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantSearch: 1, wantCreate: 1, wantSets: 1},
		{name: "cached foreign author", seed: func(f *fake.Fake) { f.SeedIssue(statusIssue("stranger")); f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 2, wantSearch: 1,
			wantCache: "2", wantSets: 1, wantLog: "reason=foreign"},
		{name: "cached missing marker", seed: func(f *fake.Fake) { f.SeedIssue(noMarker); f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 2, wantSearch: 1,
			wantCache: "2", wantSets: 1, wantLog: "status.cache_stale"},
		{name: "cached retitled", seed: func(f *fake.Fake) { f.SeedIssue(retitled); f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 2, wantSearch: 1,
			wantCache: "2", wantSets: 1, wantLog: "reason=foreign"},
		{name: "cached number is a PR", seed: func(f *fake.Fake) {
			f.SeedPR(github.PullRequest{Title: StatusTitle, Body: StatusMarker, Author: bot, State: github.StateOpen})
			f.SeedIssue(own)
		}, cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, wantNum: 2, wantSearch: 1,
			wantCache: "2", wantSets: 1, wantLog: "status.cache_stale"},
		{name: "cached not found", seed: func(f *fake.Fake) {},
			cache: &memCache{m: map[string]string{StatusIssueKey: "9"}}, wantSearch: 1, wantCreate: 1,
			wantSets: 1, wantLog: "reason=not_found"},
		{name: "transfer redirect", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, getNumber: func(int) int { return 7 },
			wantNum: 1, wantSearch: 1, wantCache: "1", wantLog: "status.cache_stale"},
		{name: "cached read transient error falls back", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "1"}}, failGet: errors.New("502"),
			wantNum: 1, wantSearch: 1, wantCache: "1", wantLog: "status.cache_unreadable"},
		{name: "invalid value abc", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "abc"}}, wantNum: 1, wantSearch: 1,
			wantCache: "1", wantSets: 1, wantLog: "status.cache_invalid"},
		{name: "invalid value 0", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "0"}}, wantNum: 1, wantSearch: 1,
			wantCache: "1", wantSets: 1, wantLog: "status.cache_invalid"},
		{name: "invalid value -3", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{m: map[string]string{StatusIssueKey: "-3"}}, wantNum: 1, wantSearch: 1,
			wantCache: "1", wantSets: 1, wantLog: "status.cache_invalid"},
		{name: "cache read error", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{readErr: errors.New("disk")}, wantNum: 1, wantSearch: 1,
			wantSets: 1, wantLog: "status.cache_read_failed"},
		{name: "empty cache, search hit", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{}, wantNum: 1, wantSearch: 1, wantCache: "1", wantSets: 1},
		{name: "empty cache, create", seed: func(f *fake.Fake) {},
			cache: &memCache{}, wantSearch: 1, wantCreate: 1, wantSets: 1},
		{name: "write error is a warning", seed: func(f *fake.Fake) { f.SeedIssue(own) },
			cache: &memCache{writeErr: errors.New("ro")}, wantNum: 1, wantSearch: 1,
			wantLog: "status.cache_write_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fake.New()
			tc.seed(f)
			if tc.failGet != nil {
				f.FailNext("GetIssue", tc.failGet)
			}
			gh := &lagReader{StatusReader: f, getNumber: tc.getNumber}
			log, buf := newLog()
			got, err := EnsureStatusIssue(ctx, gh, f.As(bot), tc.cache, bot, log)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantNum != 0 && got != tc.wantNum {
				t.Errorf("got #%d, want #%d", got, tc.wantNum)
			}
			if tc.wantNum == 0 && (got <= 0 || !f.Pinned(got)) {
				t.Errorf("want a new pinned issue, got #%d", got)
			}
			if gh.searches != tc.wantSearch {
				t.Errorf("searches = %d, want %d", gh.searches, tc.wantSearch)
			}
			if c := creates(f); c != tc.wantCreate {
				t.Errorf("creates = %d, want %d", c, tc.wantCreate)
			}
			if tc.cache.sets != tc.wantSets {
				t.Errorf("sets = %d, want %d", tc.cache.sets, tc.wantSets)
			}
			want := tc.wantCache
			if tc.wantNum == 0 {
				want = fmt.Sprint(got)
			}
			if want != "" && tc.cache.m[StatusIssueKey] != want {
				t.Errorf("cache = %q, want %q", tc.cache.m[StatusIssueKey], want)
			}
			if tc.wantLog != "" && !strings.Contains(buf.String(), tc.wantLog) {
				t.Errorf("log lacks %q:\n%s", tc.wantLog, buf.String())
			}
		})
	}
	t.Run("dry-run stores nothing", func(t *testing.T) {
		f := fake.New()
		log, _ := newLog()
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: log})
		if err != nil {
			t.Fatal(err)
		}
		c := &memCache{}
		got, err := EnsureStatusIssue(ctx, f, d, c, bot, log)
		if err != nil || got >= 0 || c.sets != 0 || len(f.Writes()) != 0 {
			t.Fatalf("got %d, %v, sets %d, writes %v", got, err, c.sets, f.Writes())
		}
	})
}

func TestEnsureStatusIssueLaggingSearchRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := fake.New()
	fx := f.As(bot)
	gh := &lagReader{StatusReader: f, lag: true}
	log, _ := newLog()
	run := func(cached bool) int {
		t.Helper()
		var c StatusCache
		if cached {
			st, err := state.Open(ctx, dir, state.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			c = st
		}
		n, err := EnsureStatusIssue(ctx, gh, fx, c, bot, log)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	a, b := run(true), run(true)
	if a != b || creates(f) != 1 {
		t.Fatalf("got #%d then #%d, %d creates; want one issue", a, b, creates(f))
	}
	if c := run(false); c == a || creates(f) != 2 {
		t.Errorf("without the cache, lag should duplicate: got #%d, %d creates", c, creates(f))
	}
}
