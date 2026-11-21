package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Run executes ard with the given args (os.Args[1:] in production),
// wiring the Handlers into the command tree and mapping the result to an
// exit code: 0 ok, 1 runtime or not-implemented error, 2 usage error.
// Handler errors print once to stderr; usage errors also get a hint line.
func Run(args []string, stdout, stderr io.Writer, h Handlers, version string) int {
	app := &App{Handlers: h, Version: version}
	root := NewRoot(app)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		fmt.Fprintf(stderr, "%v\n", ee.Err)
		return ee.Code
	}
	fmt.Fprintf(stderr, "ard: %v\nRun \"ard --help\" for usage.\n", err)
	return 2
}
