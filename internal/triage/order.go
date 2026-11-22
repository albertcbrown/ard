package triage

import (
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/labels"
)

// heldBy reports the first prerequisite of role that is requested but not done
// on an issue with labels have. A prerequisite p is requested when its request
// label (ard:pending for the lead) or its :running label is present, and done
// when its :done label is present. A prerequisite that was never requested
// does not hold. Entries equal to role.Name are ignored (config rejects them;
// this is defensive). Pure.
func heldBy(role config.Role, have []string) (dep string, held bool) {
	for _, p := range role.After {
		if p == role.Name {
			continue
		}
		requested := hasLabel(have, labels.Request(labels.StageTriage, p)) || hasLabel(have, labels.Running(labels.StageTriage, p))
		if requested && !hasLabel(have, labels.Done(labels.StageTriage, p)) {
			return p, true
		}
	}
	return "", false
}
