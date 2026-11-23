// Package janitor prunes the .ard/<issue#>/ directories of issues closed
// long ago (#61). Git history keeps the audit trail. It is a plain controller
// loop: no slot, executor or worktree. Every deletion goes through the main
// committer, whose path guard confines a request to its own issue's
// directory, so .ard/config.yaml and the role directories can never be hit.
package janitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

const (
	Name = "janitor"
	// Retention is how long an issue must have been closed before its
	// directory is pruned.
	Retention = 90 * 24 * time.Hour
	// Interval is how often the loop wakes; Period is how often it works.
	Interval = 24 * time.Hour
	Period   = 30 * 24 * time.Hour
)

// Reader is the read side, satisfied by github.Reader.
type Reader interface {
	SearchIssues(ctx context.Context, q string, o github.SearchOpts) ([]github.Issue, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
}

// Committer commits to main, satisfied by the shared main committer.
type Committer interface {
	Commit(ctx context.Context, req committer.Request) (committer.Result, error)
}

// Tree lists every file path under .ard/ at the latest main.
type Tree func(ctx context.Context) ([]string, error)

// Janitor is the pruning loop.
type Janitor struct {
	gh   Reader
	cm   Committer
	tree Tree
	now  func() time.Time

	mu      sync.Mutex
	lastRun time.Time
}

var _ controller.Loop = (*Janitor)(nil)

// New returns a Janitor; a nil now means time.Now.
func New(gh Reader, cm Committer, tree Tree, now func() time.Time) *Janitor {
	if now == nil {
		now = time.Now
	}
	return &Janitor{gh: gh, cm: cm, tree: tree, now: now}
}

func (j *Janitor) Name() string                            { return Name }
func (j *Janitor) Interval(*config.Resolved) time.Duration { return Interval }

// Claims is true so that pause stops pruning.
func (j *Janitor) Claims() bool { return true }

var issueDirRe = regexp.MustCompile(`^\.ard/([1-9][0-9]*)/`)

// Tick runs a pass when Period has passed since the last completed one. The
// pass is idempotent, so losing lastRun on a restart costs one extra scan.
func (j *Janitor) Tick(ctx context.Context, env controller.Env) error {
	now := j.now()
	j.mu.Lock()
	last := j.lastRun
	j.mu.Unlock()
	if !last.IsZero() && now.Sub(last) < Period {
		return nil
	}
	if err := j.pass(ctx, env.Log, now); err != nil {
		return err
	}
	j.mu.Lock()
	j.lastRun = now
	j.mu.Unlock()
	return nil
}

func (j *Janitor) pass(ctx context.Context, log *slog.Logger, now time.Time) error {
	paths, err := j.tree(ctx)
	if err != nil {
		return fmt.Errorf("janitor: list .ard: %w", err)
	}
	files := map[int][]string{}
	for _, p := range paths {
		if m := issueDirRe.FindStringSubmatch(p); m != nil {
			n, _ := strconv.Atoi(m[1])
			files[n] = append(files[n], p)
		}
	}
	if len(files) == 0 {
		return nil
	}
	cutoff := now.Add(-Retention).UTC().Format("2006-01-02")
	cands, err := j.gh.SearchIssues(ctx, "is:issue is:closed closed:<"+cutoff, github.SearchOpts{Sort: "created", Order: "asc", Max: 1000})
	if err != nil {
		return fmt.Errorf("janitor: search: %w", err)
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].Number < cands[b].Number })

	var errs []error
	pruned := 0
	for _, c := range cands {
		fs := files[c.Number]
		if len(fs) == 0 {
			continue
		}
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		// Re-fetch: the issue may have been reopened since the search.
		cur, err := j.gh.GetIssue(ctx, c.Number)
		if err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: get: %w", c.Number, err))
			continue
		}
		if cur.State != github.StateClosed || cur.IsPR || cur.ClosedAt == nil || now.Sub(*cur.ClosedAt) <= Retention {
			continue
		}
		if hasAny(cur.Labels, labels.Hold, labels.Keep) {
			log.Info("janitor skip", "issue", cur.Number, "reason", "hold or keep label")
			continue
		}
		if err := j.prune(ctx, cur.Number, fs); err != nil {
			log.Error("janitor failed", "issue", cur.Number, "error", err)
			errs = append(errs, fmt.Errorf("issue #%d: %w", cur.Number, err))
			continue
		}
		pruned++
		log.Info("janitor pruned", "issue", cur.Number, "files", len(fs))
	}
	if len(errs) > 0 {
		return fmt.Errorf("janitor: %d issues failed (%d pruned): %w", len(errs), pruned, errors.Join(errs...))
	}
	return nil
}

func (j *Janitor) prune(ctx context.Context, n int, paths []string) error {
	sort.Strings(paths)
	for len(paths) > 0 {
		k := min(len(paths), committer.MaxFiles)
		fs := make([]committer.File, k)
		for i, p := range paths[:k] {
			fs[i] = committer.File{Path: p, Delete: true}
		}
		paths = paths[k:]
		_, err := j.cm.Commit(ctx, committer.Request{
			Issue: n, Stage: "janitor", Role: "prune",
			Summary: "prune closed issue directory", Files: fs,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func hasAny(have []string, want ...string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}
