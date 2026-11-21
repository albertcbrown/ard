package bad

import (
	"context"
	"os/exec"
)

func R5() error { return exec.CommandContext(context.Background(), "git", "log").Run() }
