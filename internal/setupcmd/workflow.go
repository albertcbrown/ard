package setupcmd

import (
	"bytes"
	"context"
	_ "embed"
	"io"
)

// botCommitsWorkflow is a copy of .github/workflows/bot-commits.yml; a test
// keeps the two equal. Setup prints it for a human to commit and never
// commits or writes it.
//
//go:embed workflow/bot-commits.yml
var botCommitsWorkflow []byte

const (
	workflowPath = ".github/workflows/bot-commits.yml"
	workflowName = "workflow"
)

// PrintWorkflow writes the embedded bot-commits workflow to w.
func PrintWorkflow(w io.Writer) error {
	_, err := w.Write(botCommitsWorkflow)
	return err
}

const workflowFix = "commit " + workflowPath + ": run ard setup --print-workflow > " + workflowPath + ", then open a PR"

// checkWorkflow compares the workflow on origin/main with the embedded copy.
// It never fails: every problem is a manual row.
func checkWorkflow(ctx context.Context, at func(ctx context.Context) ([]byte, bool, error)) Row {
	manual := func(detail string) Row { return Row{Name: workflowName, Status: StatusManual, Detail: detail} }
	got, found, err := at(ctx)
	switch {
	case err != nil:
		return manual("could not read " + workflowPath + " on origin/main (fetch first?): " + err.Error() + "; " + workflowFix)
	case !found:
		return manual(workflowPath + " is not on origin/main; " + workflowFix)
	}
	norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Equal(norm(got), norm(botCommitsWorkflow)) {
		return manual(workflowPath + " on origin/main differs from the copy embedded in ard; " + workflowFix)
	}
	return Row{Name: workflowName, Status: StatusOK, Detail: "matches the copy embedded in ard"}
}
