package agentjob

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

// InputDir is where an issue's untrusted inputs go, relative to the worktree.
// It is outside .ard/<n>/, the only path close-out collects, so it is never
// committed.
func InputDir(n int) string { return fmt.Sprintf(".ard-input/%d", n) }

// HandleSaver is the part of *state.Store SaveHandle uses.
type HandleSaver interface {
	SetHandle(ctx context.Context, id string, h json.RawMessage) error
}

// SaveHandle stores h on the job so the Reaper watches the current process. A
// failure is logged, not returned: the run continues and the Reaper falls back
// to the record it has.
func SaveHandle(ctx context.Context, s HandleSaver, log *slog.Logger, jobID string, h executor.Handle) {
	b, err := json.Marshal(h)
	if err == nil {
		err = s.SetHandle(ctx, jobID, b)
	}
	if err != nil {
		log.Error("job.handle_not_saved", "error", err)
	}
}

// Wait is executor.WaitFor that also returns when stopping closes. On
// shutdown the job keeps running (the executor is not told to stop) and the
// error is context.Canceled.
func Wait(ctx context.Context, ex executor.Executor, h executor.Handle, every time.Duration, stopping <-chan struct{}) (executor.Status, error) {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-stopping:
			cancel()
		case <-wctx.Done():
		}
	}()
	return executor.WaitFor(wctx, ex, h, every)
}

// CommentReader lists the comments of an issue.
type CommentReader interface {
	ListComments(ctx context.Context, n int, since time.Time) ([]github.Comment, error)
}

// CommentWriter creates a comment.
type CommentWriter interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
}

// PostOnce posts text with marker as its first line unless a comment already
// starts with marker. The marker makes a replay after a crash safe.
func PostOnce(ctx context.Context, r CommentReader, w CommentWriter, n int, marker, text string) error {
	cs, err := r.ListComments(ctx, n, time.Time{})
	if err != nil {
		return fmt.Errorf("issue %d: list comments: %w", n, err)
	}
	for _, c := range cs {
		if strings.HasPrefix(c.Body, marker) {
			return nil
		}
	}
	if _, err := w.CreateComment(ctx, n, marker+"\n"+text); err != nil {
		return fmt.Errorf("issue %d: comment: %w", n, err)
	}
	return nil
}

// AdmissionStore is the part of *state.Store MarkRegated uses.
type AdmissionStore interface {
	Admission(ctx context.Context, issue int) (state.Admission, error)
	SetAdmission(ctx context.Context, a state.Admission) error
}

// MarkRegated records, the first time an issue is re-gated for a hash, the
// issue's updated_at: a re-admitting relabel must come after it.
func MarkRegated(ctx context.Context, st AdmissionStore, is github.Issue, hash string) error {
	a, err := st.Admission(ctx, is.Number)
	if err != nil {
		return fmt.Errorf("issue %d: read admission: %w", is.Number, err)
	}
	if a.RegatedHash == hash && !a.RegatedAt.IsZero() {
		return nil
	}
	a.RegatedAt, a.RegatedHash = is.UpdatedAt, hash
	if err := st.SetAdmission(ctx, a); err != nil {
		return fmt.Errorf("issue %d: %w", is.Number, err)
	}
	return nil
}
