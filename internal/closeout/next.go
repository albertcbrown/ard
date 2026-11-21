package closeout

import (
	"fmt"
	"regexp"

	"github.com/albertcbrown/ard/internal/config"
)

// maxNext bounds how many next entries one role file may request.
const maxNext = 8

var nextRe = regexp.MustCompile(`^(triage|review):([a-z0-9][a-z0-9-]*)$`)

// NextReq is one accepted next entry.
type NextReq struct{ Stage, Role string }

func (n NextReq) String() string { return n.Stage + ":" + n.Role }

// Skip is a next entry that was not applied, with the reason.
type Skip struct{ Entry, Why string }

// RoleSet answers whether a role directory exists. *config.Resolved
// satisfies it.
type RoleSet interface {
	Role(k config.Kind, name string) (config.Role, bool)
}

// kindOf maps a label stage to the config kind of its roles: stage review is
// kind reviewer.
func kindOf(stage string) (config.Kind, bool) {
	switch stage {
	case StageTriage:
		return config.KindTriage, true
	case StageWorker:
		return config.KindWorker, true
	case StageReview:
		return config.KindReviewer, true
	}
	return "", false
}

func hasRole(rs RoleSet, stage, name string) bool {
	k, ok := kindOf(stage)
	if !ok || rs == nil {
		return false
	}
	_, ok = rs.Role(k, name)
	return ok
}

// resolveNext sorts the raw entries of a done role file into requests to
// apply now, review requests deferred until a PR exists, and skips.
func resolveNext(j Job, entries []string, rs RoleSet) (now, deferred []NextReq, skipped []Skip) {
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e] {
			skipped = append(skipped, Skip{e, "duplicate"})
			continue
		}
		seen[e] = true
		if len(seen) > maxNext {
			skipped = append(skipped, Skip{clip(e, 60), fmt.Sprintf("limit %d", maxNext)})
			continue
		}
		m := nextRe.FindStringSubmatch(e)
		if m == nil {
			skipped = append(skipped, Skip{clip(e, 60), "bad grammar"})
			continue
		}
		req := NextReq{m[1], m[2]}
		switch {
		case j.Stage == StageReview:
			skipped = append(skipped, Skip{e, "wrong stage"})
		case req.Stage == StageTriage && j.Stage != StageTriage:
			skipped = append(skipped, Skip{e, "wrong stage"})
		case req.Stage == StageTriage && req.Role == "lead":
			skipped = append(skipped, Skip{e, "not requestable"})
		case req.Stage == StageTriage && req.Role == j.Role:
			skipped = append(skipped, Skip{e, "self request"})
		case !hasRole(rs, req.Stage, req.Role):
			skipped = append(skipped, Skip{e, "no such role"})
		case req.Stage == StageReview && j.Stage == StageTriage:
			deferred = append(deferred, req)
		case req.Stage == StageReview && j.PR <= 0:
			skipped = append(skipped, Skip{e, "no PR yet"})
		default:
			now = append(now, req)
		}
	}
	return now, deferred, skipped
}
