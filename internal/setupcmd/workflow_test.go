package setupcmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/cli"
)

func TestEmbeddedWorkflowMatchesRepo(t *testing.T) {
	b, err := os.ReadFile("../../" + workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, botCommitsWorkflow) {
		t.Error("internal/setupcmd/workflow/bot-commits.yml differs from " + workflowPath)
	}
}

func TestCheckWorkflow(t *testing.T) {
	crlf := bytes.ReplaceAll(botCommitsWorkflow, []byte("\n"), []byte("\r\n"))
	cases := []struct {
		name  string
		b     []byte
		found bool
		err   error
		want  Status
		sub   string
	}{
		{"absent", nil, false, nil, StatusManual, "is not on origin/main"},
		{"same", botCommitsWorkflow, true, nil, StatusOK, ""},
		{"crlf", crlf, true, nil, StatusOK, ""},
		{"edited", append([]byte("# x\n"), botCommitsWorkflow...), true, nil, StatusManual, "differs from the copy embedded"},
		{"no origin", nil, false, errors.New("origin/main not found"), StatusManual, "--print-workflow"},
		{"too big", nil, false, errors.New("too large"), StatusManual, "too large"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := checkWorkflow(context.Background(), func(context.Context) ([]byte, bool, error) { return c.b, c.found, c.err })
			if r.Status != c.want || !strings.Contains(r.Detail, c.sub) {
				t.Errorf("row %+v", r)
			}
		})
	}
}

func TestPrintWorkflow(t *testing.T) {
	var b bytes.Buffer
	if err := PrintWorkflow(&b); err != nil || !bytes.Equal(b.Bytes(), botCommitsWorkflow) {
		t.Errorf("err %v", err)
	}
}

func TestCommandPrintWorkflowNeedsNothing(t *testing.T) {
	var out bytes.Buffer
	e := &cli.Env{Globals: cli.Globals{ConfigPath: "/nonexistent/config.yaml"}, Log: quiet, Stdout: &out, Stderr: &bytes.Buffer{}}
	if err := Command(context.Background(), e, cli.SetupOpts{PrintWorkflow: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), botCommitsWorkflow) {
		t.Error("stdout is not exactly the workflow")
	}
}
