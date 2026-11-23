package effects

import (
	"context"
	"crypto/sha1" //nolint:gosec // identifier hash, not security
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

// DryRunOptions configures NewDryRun.
type DryRunOptions struct {
	Log        *slog.Logger
	LedgerPath string                                                  // "" = in-memory counter, no ledger file
	Redact     func(string) string                                     // nil = identity; #21 plugs in later
	Staged     func(ctx context.Context, dir string) ([]string, error) // usually (*gitx.Git).StagedPaths; nil = skip
}

// DryRun logs and records effects without performing any. It holds no writer.
type DryRun struct {
	log    *slog.Logger
	redact func(string) string
	staged func(ctx context.Context, dir string) ([]string, error)

	mu     sync.Mutex
	next   int // next synthetic number, always negative
	ledger ledgerFile
	path   string // ledger file, "" when in memory
	broken error  // set when the ledger could not be repaired; fails every later effect
}

var _ Effects = (*DryRun)(nil)

// NewDryRun builds a DryRun, recovering the synthetic counter from the ledger.
func NewDryRun(o DryRunOptions) (*DryRun, error) {
	d := &DryRun{log: o.Log, redact: o.Redact, staged: o.Staged, next: -1}
	if d.log == nil {
		d.log = slog.Default()
	}
	if d.redact == nil {
		d.redact = func(s string) string { return s }
	}
	if o.LedgerPath != "" {
		f, next, err := openLedger(o.LedgerPath)
		if err != nil {
			return nil, err
		}
		d.ledger, d.next, d.path = f, next, o.LedgerPath
	}
	return d, nil
}

// Close releases the ledger file.
func (d *DryRun) Close() error {
	if d.ledger == nil {
		return nil
	}
	return d.ledger.Close()
}

func (d *DryRun) DryRun() bool { return true }

// emit logs and records one effect. If alloc, it assigns the next synthetic
// number and passes it to result.
func (d *DryRun) emit(ctx context.Context, op, target string, payload map[string]any, alloc bool, result func(n int) map[string]any) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("effects: %s: %w", op, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.broken != nil {
		return 0, fmt.Errorf("effects: %s: %w", op, d.broken)
	}
	n := 0
	if alloc {
		n = d.next
		d.next-- // never reissue a number, even if the append below fails
	}
	var res map[string]any
	if result != nil {
		res = result(n)
	}
	if d.ledger != nil {
		r := record{TS: time.Now().UTC(), Op: op, Target: target, Payload: payload, Result: res}
		err, broken := appendRecord(d.ledger, r)
		if broken != nil {
			d.broken = broken
		}
		if err != nil {
			return 0, fmt.Errorf("effects: %s: %w", op, err)
		}
	}
	d.log.InfoContext(ctx, "effect", "op", op, "dry_run", true, "target", target, "payload", payload, "result", res)
	return n, nil
}

func (d *DryRun) r(s string) string { return d.redact(s) }

func (d *DryRun) CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error) {
	p := map[string]any{"title": d.r(in.Title), "body": d.r(in.Body), "labels": in.Labels}
	var url string
	n, err := d.emit(ctx, "CreateIssue", "", p, true, func(n int) map[string]any {
		url = fmt.Sprintf("dry-run://issue/%d", n)
		return map[string]any{"number": n, "url": url}
	})
	if err != nil {
		return github.Issue{}, err
	}
	return github.Issue{Number: n, Title: in.Title, Body: in.Body, Labels: in.Labels, State: github.StateOpen, URL: url}, nil
}

func (d *DryRun) UpdateIssue(ctx context.Context, n int, u github.IssueUpdate) error {
	p := map[string]any{}
	if u.Title != nil {
		p["title"] = d.r(*u.Title)
	}
	if u.Body != nil {
		p["body"] = d.r(*u.Body)
	}
	if u.State != nil {
		p["state"] = string(*u.State)
	}
	if u.StateReason != nil {
		p["state_reason"] = string(*u.StateReason)
	}
	_, err := d.emit(ctx, "UpdateIssue", num(n), p, false, nil)
	return err
}

func (d *DryRun) UpdateIssueBody(ctx context.Context, n int, body string) error {
	return d.UpdateIssue(ctx, n, github.IssueUpdate{Body: &body})
}

func (d *DryRun) PinIssue(ctx context.Context, n int) error {
	_, err := d.emit(ctx, "PinIssue", num(n), nil, false, nil)
	return err
}

func (d *DryRun) AddLabels(ctx context.Context, n int, labels ...string) error {
	_, err := d.emit(ctx, "AddLabels", num(n), map[string]any{"labels": labels}, false, nil)
	return err
}

func (d *DryRun) RemoveLabel(ctx context.Context, n int, label string) error {
	_, err := d.emit(ctx, "RemoveLabel", num(n), map[string]any{"label": label}, false, nil)
	return err
}

func (d *DryRun) CreateLabel(ctx context.Context, l github.Label) error {
	_, err := d.emit(ctx, "CreateLabel", l.Name, map[string]any{"name": l.Name, "color": l.Color, "description": d.r(l.Description)}, false, nil)
	return err
}

func (d *DryRun) UpdateLabel(ctx context.Context, name string, l github.Label) error {
	_, err := d.emit(ctx, "UpdateLabel", name, map[string]any{"name": l.Name, "color": l.Color, "description": d.r(l.Description)}, false, nil)
	return err
}

func (d *DryRun) CreateComment(ctx context.Context, n int, body string) (github.Comment, error) {
	id, err := d.emit(ctx, "CreateComment", num(n), map[string]any{"body": d.r(body)}, true, func(id int) map[string]any {
		return map[string]any{"id": id}
	})
	if err != nil {
		return github.Comment{}, err
	}
	return github.Comment{ID: int64(id), Body: body}, nil
}

func (d *DryRun) CreatePR(ctx context.Context, in github.NewPR) (github.PullRequest, error) {
	p := map[string]any{"title": d.r(in.Title), "body": d.r(in.Body), "head": in.Head, "base": in.Base, "draft": in.Draft}
	var url string
	n, err := d.emit(ctx, "CreatePR", "", p, true, func(n int) map[string]any {
		url = fmt.Sprintf("dry-run://pull/%d", n)
		return map[string]any{"number": n, "url": url}
	})
	if err != nil {
		return github.PullRequest{}, err
	}
	return github.PullRequest{Number: n, Title: in.Title, Body: in.Body, HeadRef: in.Head, BaseRef: in.Base, Draft: in.Draft, State: github.StateOpen, URL: url}, nil
}

func (d *DryRun) UpdatePR(ctx context.Context, n int, u github.PRUpdate) error {
	p := map[string]any{}
	if u.Title != nil {
		p["title"] = d.r(*u.Title)
	}
	if u.Body != nil {
		p["body"] = d.r(*u.Body)
	}
	_, err := d.emit(ctx, "UpdatePR", num(n), p, false, nil)
	return err
}

func (d *DryRun) MarkReady(ctx context.Context, n int) error {
	_, err := d.emit(ctx, "MarkReady", num(n), nil, false, nil)
	return err
}

func (d *DryRun) CreateReview(ctx context.Context, n int, in github.ReviewInput) (int64, error) {
	inline := make([]map[string]any, 0, len(in.Comments))
	for _, c := range in.Comments {
		inline = append(inline, map[string]any{"path": c.Path, "line": c.Line, "side": c.Side, "body": d.r(c.Body)})
	}
	id, err := d.emit(ctx, "CreateReview", num(n), map[string]any{"commit": in.CommitSHA, "body": d.r(in.Body), "comments": inline}, true, func(id int) map[string]any {
		return map[string]any{"id": id}
	})
	return int64(id), err
}

func (d *DryRun) UpdateBranch(ctx context.Context, n int, expectedHeadSHA string) error {
	_, err := d.emit(ctx, "UpdateBranch", num(n), map[string]any{"expected_head": expectedHeadSHA}, false, nil)
	return err
}

// MergePR returns a zero MergeResult: nothing was merged.
func (d *DryRun) MergePR(ctx context.Context, n int, in github.MergeInput) (github.MergeResult, error) {
	_, err := d.emit(ctx, "MergePR", num(n), map[string]any{"sha": in.SHA, "title": d.r(in.Title), "message": d.r(in.Message)}, false, nil)
	return github.MergeResult{}, err
}

func (d *DryRun) DeleteBranch(ctx context.Context, branch string) error {
	_, err := d.emit(ctx, "DeleteBranch", branch, nil, false, nil)
	return err
}

// Commit makes no commit and leaves the index alone. With a Staged hook an
// empty index returns ErrNothingToCommit, as Live does.
func (d *DryRun) Commit(ctx context.Context, op CommitOp) (CommitResult, error) {
	var paths []string
	if d.staged != nil {
		var err error
		if paths, err = d.staged(ctx, op.Dir); err != nil {
			return CommitResult{}, fmt.Errorf("effects: Commit: staged paths: %w", err)
		}
		if len(paths) == 0 {
			return CommitResult{}, ErrNothingToCommit
		}
		sort.Strings(paths)
	}
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "%s\x00%s\x00%s", op.Dir, op.Message, strings.Join(paths, "\x00"))
	sha := hex.EncodeToString(h.Sum(nil))
	p := map[string]any{"dir": op.Dir, "message": d.r(op.Message), "author": op.Author.Name + " <" + op.Author.Email + ">", "staged": paths}
	if _, err := d.emit(ctx, "Commit", op.Dir, p, false, func(int) map[string]any { return map[string]any{"sha": sha} }); err != nil {
		return CommitResult{}, err
	}
	return CommitResult{SHA: sha, Synthetic: true}, nil
}

// Push never calls op.Token and never contacts the remote.
func (d *DryRun) Push(ctx context.Context, op PushOp) error {
	_, err := d.emit(ctx, "Push", op.Remote+" "+op.Ref, map[string]any{
		"dir": op.Dir, "remote": op.Remote, "ref": op.Ref, "force_with_lease": op.ForceWithLease}, false, nil)
	return err
}

func num(n int) string { return fmt.Sprintf("#%d", n) }
