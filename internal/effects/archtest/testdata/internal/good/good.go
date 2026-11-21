package good

import "os/exec"

// Local types with the same method names, and non-git commands, are fine.
type fx struct{}

func (fx) AddLabels() error { return nil }
func (fx) Commit() error    { return nil }
func (fx) Push() error      { return nil }

func Run() error {
	_ = fx{}.AddLabels()
	_ = fx{}.Commit()
	return exec.Command("ls").Run()
}
