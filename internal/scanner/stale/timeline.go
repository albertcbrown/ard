package stale

import (
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

// activityEvents are the timeline events that count as someone paying
// attention to an issue. Closes and edits by the App are not among them
// because the actor check excludes the App, and body edits and reactions are
// not in the timeline at all (accepted).
var activityEvents = map[string]bool{
	"commented": true, "labeled": true, "unlabeled": true,
	"assigned": true, "unassigned": true,
	"milestoned": true, "demilestoned": true,
	"renamed": true, "reopened": true,
	"cross-referenced": true, "referenced": true,
	"connected": true, "disconnected": true,
}

// lastActivity is the later of the issue's creation and the newest timeline
// event by anyone other than bot. An event with no actor counts.
func lastActivity(is github.Issue, tl []github.TimelineEvent, bot string) time.Time {
	last := is.CreatedAt
	for _, ev := range tl {
		if ev.Actor == bot || !activityEvents[ev.Event] {
			continue
		}
		if ev.CreatedAt.After(last) {
			last = ev.CreatedAt
		}
	}
	return last
}

// hasLinkedOpenPR reports whether an open PR references the issue, or more
// PRs were connected than disconnected. A connected event does not name its
// PR, so that arm is deliberately conservative.
func hasLinkedOpenPR(tl []github.TimelineEvent) bool {
	connected := 0
	for _, ev := range tl {
		switch ev.Event {
		case "cross-referenced":
			if ev.Source != nil && ev.Source.IsPR && ev.Source.State == github.StateOpen {
				return true
			}
		case "connected":
			connected++
		case "disconnected":
			connected--
		}
	}
	return connected > 0
}

// priorStaleComment reports whether the bot already left the stale comment
// since lastActive, which means an earlier tick commented and then failed to
// close. A marker from before a reopen is older than lastActive.
func priorStaleComment(tl []github.TimelineEvent, bot string, lastActive time.Time) bool {
	for _, ev := range tl {
		if ev.Event == "commented" && ev.Actor == bot && ev.CreatedAt.After(lastActive) &&
			strings.Contains(ev.Body, Marker) {
			return true
		}
	}
	return false
}
