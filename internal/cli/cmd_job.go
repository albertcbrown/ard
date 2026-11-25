package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// The job-* commands run inside a job pod of the Kubernetes executor. They
// are hidden: the pod builder writes their flags (#317).

func parseDeadline(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, &ExitError{Code: 2, Err: fmt.Errorf("--deadline: want RFC 3339 UTC time: %w", err)}
	}
	return t, nil
}

func addDeadline(cmd *cobra.Command, s *string) {
	cmd.Flags().StringVar(s, "deadline", "", "RFC 3339 UTC time by which the step must end")
	_ = cmd.MarkFlagRequired("deadline")
}

func newJobPre(app *App) *cobra.Command {
	var deadline string
	o := JobPreOpts{}
	cmd := &cobra.Command{
		Use:    "job-pre",
		Short:  "Pod init step: fetch and place the job inputs",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := parseDeadline(deadline)
			if err != nil {
				return err
			}
			o.Deadline = d
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.JobPre == nil {
					return notImpl(cmd.CommandPath(), "#316")
				}
				return app.Handlers.JobPre(ctx, e, o)
			})
		},
	}
	addDeadline(cmd, &deadline)
	cmd.Flags().StringVar(&o.GitBase, "git-base", "https://github.com", "git host base URL")
	return cmd
}

func newJobExec(app *App) *cobra.Command {
	var deadline string
	o := JobExecOpts{}
	cmd := &cobra.Command{
		Use:    "job-exec -- command [args...]",
		Short:  "Pod agent step: run the agent command and write the done marker",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := parseDeadline(deadline)
			if err != nil {
				return err
			}
			o.Deadline = d
			dash := cmd.ArgsLenAtDash()
			if dash < 0 || dash != 0 || len(args) == 0 {
				return &ExitError{Code: 2, Err: errors.New("job-exec: want the agent command after --")}
			}
			o.Argv = args
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.JobExec == nil {
					return notImpl(cmd.CommandPath(), "#316")
				}
				return app.Handlers.JobExec(ctx, e, o)
			})
		},
	}
	addDeadline(cmd, &deadline)
	cmd.Flags().DurationVar(&o.Grace, "grace", 10*time.Second, "time between SIGTERM and SIGKILL")
	return cmd
}

func newJobPost(app *App) *cobra.Command {
	var deadline string
	o := JobPostOpts{}
	cmd := &cobra.Command{
		Use:    "job-post",
		Short:  "Pod post step: serve the result to the controller",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := parseDeadline(deadline)
			if err != nil {
				return err
			}
			o.Deadline = d
			return app.run(cmd, func(ctx context.Context, e *Env) error {
				if app.Handlers.JobPost == nil {
					return notImpl(cmd.CommandPath(), "#316")
				}
				return app.Handlers.JobPost(ctx, e, o)
			})
		},
	}
	addDeadline(cmd, &deadline)
	return cmd
}
