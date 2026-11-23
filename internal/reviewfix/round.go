package reviewfix

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

// Phases and reasons of a round.
const (
	PhaseRequested = "requested"
	PhaseApplied   = "applied"
	PhaseCapped    = "capped"

	ReasonReview   = "review"
	ReasonConflict = "conflict"
)

// Round is one review_round event.
type Round struct {
	ID     int64    `json:"-"` // the event ID
	N      int      `json:"round"`
	Phase  string   `json:"phase"`
	Reason string   `json:"reason"`
	PR     int      `json:"pr"`
	Head   string   `json:"head"`
	Worker string   `json:"worker"`
	Roles  []string `json:"roles"`
}

// EventStore is the part of *state.Store the round API uses.
type EventStore interface {
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
	AppendEvent(ctx context.Context, e state.Event) error
}

func decodeRound(e state.Event) (Round, error) {
	var r Round
	if err := json.Unmarshal(e.Data, &r); err != nil {
		return Round{}, fmt.Errorf("review_round event %d: %w", e.ID, err)
	}
	r.ID = e.ID
	return r, nil
}

// Rounds returns the issue's round events in ID order. A row that does not
// decode is an error.
func Rounds(ctx context.Context, s interface {
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
}, issue int) ([]Round, error) {
	evs, err := s.Events(ctx, state.EventFilter{Kind: state.EventReviewRound, Issue: issue})
	if err != nil {
		return nil, fmt.Errorf("read review rounds of issue %d: %w", issue, err)
	}
	return decodeRounds(evs)
}

func decodeRounds(evs []state.Event) ([]Round, error) {
	var out []Round
	for _, e := range evs {
		r, err := decodeRound(e)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Requested counts the requested rounds, whatever their reason: the number
// max_review_rounds caps.
func Requested(rs []Round) int {
	n := 0
	for _, r := range rs {
		if r.Phase == PhaseRequested {
			n++
		}
	}
	return n
}

// Request describes a round to start.
type Request struct {
	Issue, PR            int
	Head, Worker, Reason string
	Roles                []string
	Max                  int
}

func (l *Loop) appendRound(ctx context.Context, issue int, r Round) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return l.d.Store.AppendEvent(ctx, state.Event{Kind: state.EventReviewRound, Issue: issue, Role: r.Worker, Data: data})
}

// RequestRound starts a round: it records a requested event first, then
// removes the worker's :done label so the worker loop claims the issue again.
// At the cap it runs the cap path and returns false.
func (l *Loop) RequestRound(ctx context.Context, r Request) (bool, error) {
	rs, err := Rounds(ctx, l.d.Store, r.Issue)
	if err != nil {
		return false, err
	}
	count := Requested(rs)
	if count >= r.Max {
		return false, l.capPath(ctx, r.Issue, r.PR, r.Head, r.Worker, count, r.Roles, fmt.Sprintf("the limit of %d review rounds is reached", r.Max))
	}
	err = l.appendRound(ctx, r.Issue, Round{N: count + 1, Phase: PhaseRequested, Reason: r.Reason, PR: r.PR, Head: r.Head, Worker: r.Worker, Roles: r.Roles})
	if err != nil {
		return false, err
	}
	if err := l.d.Effects.RemoveLabel(ctx, r.Issue, labels.Done(labels.StageWorker, r.Worker)); err != nil {
		return true, fmt.Errorf("issue %d: remove worker done label: %w", r.Issue, err)
	}
	return true, nil
}
