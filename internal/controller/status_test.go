package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
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
		got, err := EnsureStatusIssue(ctx, f, f, bot, log)
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
		got, err := EnsureStatusIssue(ctx, f, f.As(bot), bot, log)
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
		got, err := EnsureStatusIssue(ctx, f, f, bot, log)
		if err != nil || got != a || buf.count("status.duplicate") != 1 {
			t.Fatalf("got %d, %v\n%s", got, err, buf.String())
		}
	})
	t.Run("none: created and pinned", func(t *testing.T) {
		f := fake.New()
		log, _ := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, bot, log)
		if err != nil || got <= 0 || !f.Pinned(got) {
			t.Fatalf("got %d, %v", got, err)
		}
		is, _ := f.GetIssue(ctx, got)
		if is.Title != StatusTitle || len(is.Labels) != 0 {
			t.Errorf("issue = %+v", is)
		}
	})
	t.Run("pin error is a warning", func(t *testing.T) {
		f := fake.New()
		f.FailNext("PinIssue", errors.New("3 pinned already"))
		log, buf := newLog()
		got, err := EnsureStatusIssue(ctx, f, f, bot, log)
		if err != nil || got <= 0 || buf.count("status.pin_failed") != 1 {
			t.Fatalf("got %d, %v\n%s", got, err, buf.String())
		}
	})
	t.Run("search error is returned", func(t *testing.T) {
		f := fake.New()
		f.FailNext("SearchIssues", errors.New("down"))
		log, _ := newLog()
		if _, err := EnsureStatusIssue(ctx, f, f, bot, log); err == nil {
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
		got, err := EnsureStatusIssue(ctx, f, d, bot, log)
		if err != nil || got >= 0 || len(f.Writes()) != 0 {
			t.Fatalf("got %d, %v, writes %v", got, err, f.Writes())
		}
	})
}
