package closeout

// Label names follow ard:<stage>:<role>[:<state>] (docs/proposal.md, "Label
// state machine"). Stage is the label stage word: triage, worker or review.
// These helpers are local until the shared label package (#22) lands.

const (
	labelPending        = "ard:pending"
	labelNeedsHuman     = "ard:needs-human"
	labelExecutorFailed = "ard:executor:failed"
)

func labelRequest(stage, role string) string { return "ard:" + stage + ":" + role }
func labelRunning(stage, role string) string { return labelRequest(stage, role) + ":running" }
func labelDone(stage, role string) string    { return labelRequest(stage, role) + ":done" }
func labelChanges(role string) string        { return labelRequest(StageReview, role) + ":changes" }
