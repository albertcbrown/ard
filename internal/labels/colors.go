package labels

import "fmt"

type style struct{ color, desc string }

// Colours and descriptions for the role labels, by stage and state.
var roleStyles = map[Stage]map[State]style{
	StageTriage: {
		StateRequest: {"1d76db", "ard: triage %s requested"},
		StateRunning: {"0b4a8f", "ard: triage %s running"},
		StateDone:    {"c5def5", "ard: triage %s done"},
	},
	StageWorker: {
		StateRequest: {"5319e7", "ard: worker %s requested"},
		StateRunning: {"3b0f9e", "ard: worker %s running"},
		StateDone:    {"d4c5f9", "ard: worker %s done"},
	},
	StageReview: {
		StateRequest: {"0e8a16", "ard: review %s requested"},
		StateRunning: {"0a5c10", "ard: review %s running"},
		StateDone:    {"c2e0c6", "ard: review %s done"},
		StateChanges: {"e4a11b", "ard: review %s requested changes"},
	},
}

var scannerStyle = style{"006b75", "ard: filed by scanner %s"}

var staticStyles = map[string]style{
	Pending:        {"ededed", "ard: ready for triage (the lead's request)"},
	ExecutorFailed: {"b60205", "ard: job died or ran over time; remove to retry"},
	TestsFailed:    {"e99695", "ard: worker could not get tests green"},
	NeedsHuman:     {"d93f0b", "ard: blocked; automation stops until a human acts"},
	NeedsApproval:  {"fbca04", "ard: PR touches a protected path; merges after human approval"},
	Hold:           {"f9a03f", "ard: controller ignores this issue or PR"},
	Approved:       {"bfdadc", "ard: human go-ahead for the worker to start"},
	Keep:           {"cfd3d7", "ard: keep failed-job resources past 24 h"},
	Pause:          {"5c5c5c", "ard: on the status issue, stops all new claims"},
}

// intakeStyle is the custom intake label's style. It shares ard:pending's
// colour because both mean "ready for triage".
var intakeStyle = style{"ededed", "ard: intake label; a maintainer adds it to admit the issue to triage"}

func intakeLabel(name string) Label {
	return Label{Name: name, Color: intakeStyle.color, Description: intakeStyle.desc}
}

func roleLabel(s Stage, role string, st State) Label {
	sty := roleStyles[s][st]
	return Label{Name: Name(s, role, st), Color: sty.color, Description: fmt.Sprintf(sty.desc, role)}
}

func scannerLabel(name string) Label {
	return Label{Name: Scanner(name), Color: scannerStyle.color, Description: fmt.Sprintf(scannerStyle.desc, name)}
}

func staticLabel(name string) Label {
	sty := staticStyles[name]
	return Label{Name: name, Color: sty.color, Description: sty.desc}
}
