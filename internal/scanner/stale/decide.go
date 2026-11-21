package stale

import (
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

// reason says why an issue is skipped; "" means close it.
type reason string

const (
	reasonClose       reason = ""
	reasonNotCand     reason = "not-candidate"
	reasonHold        reason = "hold"
	reasonNeedsHuman  reason = "needs-human"
	reasonRunning     reason = "running"
	reasonHumanAuthor reason = "human-author"
	reasonTooNew      reason = "too-new"
	reasonLinkedPR    reason = "linked-pr"
	reasonActive      reason = "active"
)

type inputs struct {
	bot         string
	scanners    map[string]bool
	inactiveFor time.Duration
	now         time.Time
}

func hasLabel(is github.Issue, l string) bool {
	for _, x := range is.Labels {
		if x == l {
			return true
		}
	}
	return false
}

func scannerOf(is github.Issue, scanners map[string]bool) (string, bool) {
	for _, l := range is.Labels {
		if p, ok := labels.Parse(l); ok && p.Kind == labels.KindScanner && scanners[p.Role] {
			return p.Role, true
		}
	}
	return "", false
}

func running(is github.Issue) bool {
	for _, l := range is.Labels {
		l = strings.ToLower(l)
		if strings.HasPrefix(l, labels.Prefix) && strings.HasSuffix(l, ":"+string(labels.StateRunning)) {
			return true
		}
	}
	return false
}

// precheck applies every rule that needs no timeline, in order; the first
// match wins.
func precheck(is github.Issue, in inputs) reason {
	if _, ok := scannerOf(is, in.scanners); !ok || is.IsPR || is.State != github.StateOpen || !hasLabel(is, labels.Pending) {
		return reasonNotCand
	}
	switch {
	case hasLabel(is, labels.Hold):
		return reasonHold
	case hasLabel(is, labels.NeedsHuman):
		return reasonNeedsHuman
	case running(is):
		return reasonRunning
	case is.Author != in.bot:
		return reasonHumanAuthor
	case in.now.Sub(is.CreatedAt) < in.inactiveFor:
		return reasonTooNew
	}
	return reasonClose
}

// decide runs precheck, then the two timeline rules. It returns the last
// activity time for logging and for the prior-comment check.
func decide(is github.Issue, tl []github.TimelineEvent, in inputs) (reason, time.Time) {
	if r := precheck(is, in); r != reasonClose {
		return r, time.Time{}
	}
	last := lastActivity(is, tl, in.bot)
	switch {
	case hasLinkedOpenPR(tl):
		return reasonLinkedPR, last
	case in.now.Sub(last) < in.inactiveFor:
		return reasonActive, last
	}
	return reasonClose, last
}
