package scanner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// Store is one scanner's database. *scannerdb.DB satisfies it.
type Store interface {
	Findings(ctx context.Context) ([]scannerdb.Finding, error)
	Suppressions(ctx context.Context) ([]scannerdb.Suppression, error)
	Apply(ctx context.Context, b scannerdb.Batch) error
	Replace(ctx context.Context, f []scannerdb.Finding, s []scannerdb.Suppression) error
	RecordRun(ctx context.Context, r scannerdb.Run) (int64, error)
	LastRun(ctx context.Context) (scannerdb.Run, error) // scannerdb.ErrNotFound when none
}

// Stores opens a scanner's Store by name.
type Stores func(ctx context.Context, name string) (Store, error)

// RegistryStores adapts a scannerdb.Registry.
func RegistryStores(r *scannerdb.Registry) Stores {
	return func(ctx context.Context, name string) (Store, error) {
		db, err := r.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		return db, nil
	}
}

func loadSnapshot(ctx context.Context, s Store) (Snapshot, error) {
	rows, err := s.Findings(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	sups, err := s.Suppressions(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return NewSnapshot(rows, sups), nil
}

// Reader is the GitHub read side the pipeline needs. github.Reader satisfies
// it.
type Reader interface {
	ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error)
	GetIssue(ctx context.Context, n int) (github.Issue, error)
}

// Effects is the effect layer the pipeline writes through. effects.Effects
// satisfies it, so --dry-run logs instead of acting.
type Effects interface {
	CreateIssue(ctx context.Context, in github.NewIssue) (github.Issue, error)
	UpdateIssue(ctx context.Context, n int, u github.IssueUpdate) error
	UpdateIssueBody(ctx context.Context, n int, body string) error
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Committer commits files to main. *committer.Committer satisfies it.
type Committer interface {
	Commit(ctx context.Context, req committer.Request) (committer.Result, error)
}

// Engine runs a scanner and returns its findings. cfg is the tick's config:
// the scan runs at cfg.SHA with the role directory under cfg.Repo. since is
// when the run became due, for the pool's oldest-first ranking.
type Engine interface {
	Scan(ctx context.Context, cfg *config.Resolved, role config.Role, since time.Time) (ScanResult, error)
}

// ScanResult is one scan: the commit scanned and its findings.
type ScanResult struct {
	SHA      string
	Findings []Finding
}

// ErrNoSlot means the pool denied the run a job slot. The run is not
// recorded: it stays due and is offered again on the next tick.
var ErrNoSlot = errors.New("scanner: no job slot")

// ScanError is a scan that did not produce findings: a non-zero exit, a
// timeout, a vanished process or invalid output. Its stdout is discarded.
type ScanError struct {
	SHA      string
	Exit     int
	TimedOut bool
	Decode   DecodeErrors
	Err      error
}

func (e *ScanError) Error() string {
	switch {
	case e.TimedOut:
		return "scan timed out"
	case len(e.Decode) > 0:
		return e.Decode.Error()
	case e.Err != nil:
		return fmt.Sprintf("scan failed: %v", e.Err)
	}
	return fmt.Sprintf("scan exited with status %d", e.Exit)
}

func (e *ScanError) Unwrap() error { return e.Err }

// Run outcomes, recorded in the scanner's runs table.
const (
	OutcomeOK           = "ok"
	OutcomeBackpressure = "dropped_backpressure"
	OutcomeGlobalCap    = "dropped_global_cap"
	OutcomeScanFailed   = "scan_failed"
	OutcomeTimeout      = "timeout"
	OutcomeError        = "error"
	// OutcomeNoSlot, OutcomePaused and OutcomeSkipped are reported but never recorded.
	OutcomeNoSlot  = "dropped_no_slot"
	OutcomePaused  = "paused"
	OutcomeSkipped = "skipped"
)

func hasLabel(lbls []string, want string) bool {
	for _, l := range lbls {
		if strings.EqualFold(l, want) {
			return true
		}
	}
	return false
}
