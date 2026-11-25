//go:build unix

package jobexec

import (
	"os"
	"os/signal"
)

func signalIgnore(s os.Signal) { signal.Ignore(s) }
