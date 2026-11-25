// Command ard is the automated repository development controller.
// See docs/proposal.md for the design.
package main

import (
	"io"
	"os"
	"runtime/debug"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/configcmd"
	"github.com/albertcbrown/ard/internal/initcmd"
	"github.com/albertcbrown/ard/internal/jobexec"
	"github.com/albertcbrown/ard/internal/jobpost"
	"github.com/albertcbrown/ard/internal/jobpre"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
	"github.com/albertcbrown/ard/internal/servecmd"
	"github.com/albertcbrown/ard/internal/setupcmd"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	info, ok := debug.ReadBuildInfo()
	return cli.Run(args, stdout, stderr, handlers(), resolveVersion(version, info, ok))
}

// resolveVersion prefers the -X main.version value. Without it, a binary
// from "go install ...@vX.Y.Z" carries the tag only in its build info.
func resolveVersion(v string, info *debug.BuildInfo, ok bool) string {
	if v != "dev" {
		return v
	}
	if ok && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
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
		Setup:        setupcmd.Command,
		JobPre:       jobpre.Run,
		JobExec:      jobexec.Run,
		JobPost:      jobpost.Run,
	}
}
