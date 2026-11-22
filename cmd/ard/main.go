// Command ard is the automated repository development controller.
// See docs/proposal.md for the design.
package main

import (
	"io"
	"os"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/configcmd"
	"github.com/albertcbrown/ard/internal/initcmd"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
	"github.com/albertcbrown/ard/internal/servecmd"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr, handlers(), version)
}

// handlers wires one function per subcommand. Each new command adds one
// line here; a nil field runs the command as a "not implemented" stub.
func handlers() cli.Handlers {
	return cli.Handlers{
		ConfigCheck:  configcmd.Check,
		Serve:        servecmd.Serve,
		LabelsSync:   labelsync.Command,
		RebuildState: servecmd.RebuildState,
		Init:         initcmd.Init,
	}
}
