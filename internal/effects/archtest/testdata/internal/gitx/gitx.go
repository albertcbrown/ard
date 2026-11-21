package gitx

import "os/exec"

type Git struct{}

func (g *Git) Commit() error { return exec.Command("git", "commit").Run() }
func (g *Git) Push() error   { return exec.Command("git", "push").Run() }
