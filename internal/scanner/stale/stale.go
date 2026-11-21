// Package stale closes scanner issues nobody picked up. It is a plain
// controller loop: no slot, executor, worktree or state of its own. It only
// comments and closes as not planned; the owning scanner's next reconcile
// sees the not-planned close and suppresses the fingerprints.
package stale

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

const (
	Name     = "stale"
	Interval = 24 * time.Hour
	// Marker tags the closing comment so a retry after a failed close does
	// not comment twice.
	Marker = "<!-- ard:stale -->"
)

// ErrNoBotIdentity is returned, with no writes, when the client is not
// authenticated as a GitHub App: otherwise a token user's own issues would
// count as bot-authored.
var ErrNoBotIdentity = errors.New("stale: no GitHub App bot identity (token auth?)")

// Reader is the read side, satisfied by github.Reader.
type Reader interface {
	Identity(ctx context.Context) (github.Identity, error)
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
	ListTimeline(ctx context.Context, n int) ([]github.TimelineEvent, error)
}

// Writer is the write side, satisfied by effects.Effects.
type Writer interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	UpdateIssue(ctx context.Context, n int, u github.IssueUpdate) error
}

// Closer is the stale-closer loop.
type Closer struct {
	gh  Reader
	fx  Writer
	now func() time.Time
}

var _ controller.Loop = (*Closer)(nil)

// New returns a Closer; a nil now means time.Now.
func New(gh Reader, fx Writer, now func() time.Time) *Closer {
	if now == nil {
		now = time.Now
	}
	return &Closer{gh: gh, fx: fx, now: now}
}

func (c *Closer) Name() string                            { return Name }
func (c *Closer) Interval(*config.Resolved) time.Duration { return Interval }

// Claims is true so that pause stops stale closes.
func (c *Closer) Claims() bool { return true }

func (c *Closer) Tick(ctx context.Context, env controller.Env) error {
	return c.run(ctx, env.Log, env.Cfg)
}

func (c *Closer) run(ctx context.Context, log *slog.Logger, cfg *config.Resolved) error {
	id, err := c.gh.Identity(ctx)
	if err != nil || id.Kind != github.AuthApp || id.Login == "" {
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNoBotIdentity, err)
		}
		return ErrNoBotIdentity
	}
	scanners := map[string]bool{}
	for _, r := range cfg.RolesOf(config.KindScanner) {
		scanners[r.Name] = true
	}
	in := inputs{
		bot: id.Login, scanners: scanners,
		inactiveFor: time.Duration(cfg.Stale.InactiveFor), now: c.now(),
	}
	q := fmt.Sprintf(`is:open label:"%s" -label:"%s" -label:"%s"`, labels.Pending, labels.NeedsHuman, labels.Hold)
	issues, err := c.gh.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 1000})
	if err != nil {
		return fmt.Errorf("stale: search: %w", err)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })

	var errs []error
	for _, is := range issues {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := c.one(ctx, log, is, in, cfg.Stale); err != nil {
			log.Error("stale failed", "issue", is.Number, "error", err)
			errs = append(errs, fmt.Errorf("issue #%d: %w", is.Number, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("stale: %d of %d issues failed: %w", len(errs), len(issues), errors.Join(errs...))
	}
	return nil
}

func (c *Closer) one(ctx context.Context, log *slog.Logger, is github.Issue, in inputs, st config.Stale) error {
	sc, _ := scannerOf(is, in.scanners)
	skip := func(r reason, last time.Time) {
		log.Info("stale decision", "issue", is.Number, "scanner", sc, "action", "skip", "reason", string(r), "last_activity", last)
	}
	if r := precheck(is, in); r != reasonClose {
		skip(r, time.Time{})
		return nil
	}
	tl, err := c.gh.ListTimeline(ctx, is.Number)
	if err != nil {
		return fmt.Errorf("timeline: %w", err)
	}
	r, last := decide(is, tl, in)
	if r != reasonClose {
		skip(r, last)
		return nil
	}
	// Re-fetch: a claim or a human label may have raced the search.
	cur, err := c.gh.GetIssue(ctx, is.Number)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if r := precheck(cur, in); r != reasonClose {
		skip(r, last)
		return nil
	}
	log.Info("stale decision", "issue", is.Number, "scanner", sc, "action", "close", "reason", "", "last_activity", last)
	if !priorStaleComment(tl, in.bot, last) {
		if _, err := c.fx.CreateComment(ctx, is.Number, commentBody(st)); err != nil {
			return fmt.Errorf("comment: %w", err)
		}
	}
	closed, np := github.StateClosed, github.ReasonNotPlanned
	if err := c.fx.UpdateIssue(ctx, is.Number, github.IssueUpdate{State: &closed, StateReason: &np}); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

func commentBody(st config.Stale) string {
	return fmt.Sprintf("%s\nNo activity for %s while waiting for triage, so ard is closing this as not planned.\n"+
		"Its findings will not be refiled for %s. Reopen this issue if it still matters.\n",
		Marker, st.InactiveFor, st.SuppressFor)
}
