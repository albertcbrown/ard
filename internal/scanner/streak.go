package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// streakKey is the scanner meta key holding the reason class of the open
// failure streak that has been posted. Absent or empty = no open streak.
const streakKey = "failure_streak"

const maxClassLen = 64

// classOf maps a recorded run to its streak effect. failing starts or
// continues a streak with the given class; ok ends one; neither = neutral.
// The class is a rule name or a fixed word, never finding text.
func classOf(res RunResult) (class string, failing, ok bool) {
	switch res.Outcome {
	case OutcomeBackpressure:
		return "max_open", true, false
	case OutcomeScanFailed:
		if res.GuardRule != "" {
			return sanitiseClass(res.GuardRule), true, false
		}
		return "scan_failed", true, false
	case OutcomeTimeout:
		return "scan_failed", true, false
	case OutcomeOK:
		return "", false, true
	}
	return "", false, false
}

// sanitiseClass keeps [A-Za-z0-9._:-], at most 64 characters.
func sanitiseClass(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxClassLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == ':', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "scan_failed"
	}
	return b.String()
}

func streakNotice(scanner, class string) string {
	return fmt.Sprintf("Scanner `%s` is failing: `%s`. Its runs are not completing normally; see the controller log. One notice is posted per failure streak.", scanner, class)
}

func streakRecovery(scanner string) string {
	return fmt.Sprintf("Scanner `%s` recovered: its last run completed normally.", scanner)
}

// noteRun posts one notice when a scanner's failure streak starts and one when
// it ends. Errors are logged, never returned: a notice must not change the
// run's result.
func (p *Pipeline) noteRun(ctx context.Context, store Store, role config.Role, res RunResult) {
	log := p.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	log = log.With("scanner", role.Name)
	class, failing, ok := classOf(res)
	if !failing && !ok {
		return
	}
	ctx = context.WithoutCancel(ctx)
	open, err := store.Meta(ctx, streakKey)
	if err != nil && !errors.Is(err, scannerdb.ErrNotFound) {
		log.Error("scanner.streak_read_failed", "error", err)
		return
	}
	if failing && open != "" || ok && open == "" {
		return
	}
	var n int
	if p.StatusIssue != nil {
		n = p.StatusIssue()
	}
	if n == 0 {
		log.Info("scanner.streak_unposted", "reason", "no status issue", "class", class)
		return
	}
	body, next := streakRecovery(role.Name), ""
	if failing {
		body, next = streakNotice(role.Name, class), class
	}
	if _, err := p.Effects.CreateComment(ctx, n, body); err != nil {
		log.Error("scanner.streak_post_failed", "error", err)
		return
	}
	if p.Effects.DryRun() {
		log.Info("scanner.streak_notice", "class", class, "recovered", ok)
		return // a dry run never claims a streak it did not post
	}
	if err := store.SetMeta(ctx, streakKey, next); err != nil {
		log.Error("scanner.streak_state_failed", "error", err)
	}
}
