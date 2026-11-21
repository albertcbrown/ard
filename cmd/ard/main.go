// Command ard is the automated repository development controller.
// See docs/proposal.md for the design.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `ard - automated repository development

Usage:
  ard <command> [flags]

Commands:
  version    Print the ard version

Planned commands (see GitHub issues): serve, config check, labels sync,
rebuild-state, init.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ard %s\n", version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "ard: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
