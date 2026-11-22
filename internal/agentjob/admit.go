package agentjob

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// GateEffects is the part of effects.Effects the re-check writes with.
type GateEffects interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	AddLabels(ctx context.Context, n int, labels ...string) error
}

// Recheck is the admission re-check a loop runs before an agent that reads a
// human-filed issue. Triage admitted the issue for one title and body; if
// either changed since, the later stage must not read the new text: the issue
// is re-gated (one comment, then ard:needs-human) and a maintainer decides.
type Recheck struct {
	Stage   string // "worker" or "review": names the log events, marker and comment
	Stopped string // what ard stopped before, for the comment: "changing code", "reviewing"
	Label   string // the label that admits an issue again (intake.Label)
	Self    string // the App's login: its own issues need no admission
	Dry     bool
	Log     *slog.Logger
	Store   AdmissionStore
	Reader  CommentReader
	Effects GateEffects
}

// Admitted reports whether the agent may read issue is. An issue the App
// opened (a scanner's) is admitted; a human's needs a stored admission whose
// body hash still matches. A missing admission or a changed body is not
// admitted, and a changed body also re-gates the issue (a no-op in dry-run).
// An error means no decision: the caller tries again next poll.
func (r Recheck) Admitted(ctx context.Context, is github.Issue) (bool, error) {
	n := is.Number
	if strings.EqualFold(is.Author, r.Self) {
		return true, nil
	}
	hash := BodyHash(is)
	stored, err := r.Store.Admission(ctx, n)
	switch {
	case errors.Is(err, state.ErrNotFound):
		r.Log.Info(r.Stage+".not_admitted", "issue", n)
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%s issue %d: read admission: %w", r.Stage, n, err)
	case stored.BodyHash == hash:
		return true, nil
	}
	return false, r.regate(ctx, is, hash)
}

// regate parks an issue whose title or body changed after it was admitted.
func (r Recheck) regate(ctx context.Context, is github.Issue, hash string) error {
	n := is.Number
	if r.Dry {
		r.Log.Info(r.Stage+".would_regate", "issue", n)
		return nil
	}
	if err := MarkRegated(ctx, r.Store, is, hash); err != nil {
		return fmt.Errorf("%s: %w", r.Stage, err)
	}
	marker := fmt.Sprintf("<!-- %s%s regate body=%s -->", labels.Prefix, r.Stage, hash[:12])
	text := fmt.Sprintf("**ard · %s · paused**\n\nThe title or body of this issue changed after it was admitted, so ard stopped before %s. "+
		"A maintainer can review the change, re-apply `%s` (which runs triage again) and remove `%s` to continue.", r.Stage, r.Stopped, r.Label, labels.NeedsHuman)
	if err := PostOnce(ctx, r.Reader, r.Effects, n, marker, text); err != nil {
		return fmt.Errorf("%s: %w", r.Stage, err)
	}
	if err := r.Effects.AddLabels(ctx, n, labels.NeedsHuman); err != nil {
		return fmt.Errorf("%s issue %d: add %s: %w", r.Stage, n, labels.NeedsHuman, err)
	}
	r.Log.Info(r.Stage+".regate", "issue", n)
	return nil
}
