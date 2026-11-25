package servecmd

import (
	"errors"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
)

// UI modes of `ard serve`.
const (
	uiAuto = ""    // draw the command center only on a terminal
	uiOn   = "on"  // --ui
	uiOff  = "off" // --no-ui
)

func isTTY(v any) bool {
	f, ok := v.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// uiWanted says whether serve draws the command center. Only a real terminal
// on both stdin and stdout, with a TERM that can draw, gets it; anything else
// keeps today's output. --ui on a terminal that cannot draw is an error.
func uiWanted(mode string, stdin io.Reader, stdout io.Writer, getenv func(string) string) (bool, error) {
	if mode == uiOff {
		return false, nil
	}
	t := getenv("TERM")
	if isTTY(stdin) && isTTY(stdout) && t != "" && t != "dumb" {
		return true, nil
	}
	if mode == uiOn {
		return false, errors.New("--ui needs a terminal on stdin and stdout and a TERM that is not dumb")
	}
	return false, nil
}
