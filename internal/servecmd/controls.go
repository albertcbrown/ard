package servecmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/albertcbrown/ard/internal/config"
)

// controls implements ui.Controls: the local pause file and the runtime slot
// override. Both are local controls, so they apply in dry-run too.
type controls struct {
	w         *wiring
	pausePath string
	loops     []string
}

func (c *controls) cfgMax() int {
	if cfg := c.w.cfg.Load(); cfg != nil {
		return cfg.MaxPods
	}
	return config.DefaultMaxPods
}

// Pause creates the pause file; an existing path is success.
func (c *controls) Pause() error {
	if err := os.MkdirAll(filepath.Dir(c.pausePath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(c.pausePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return f.Close()
}

// Unpause removes the pause file and wakes the loops.
func (c *controls) Unpause() error {
	fi, err := os.Lstat(c.pausePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("pause path is a directory: remove it by hand")
	}
	if err := os.Remove(c.pausePath); err != nil {
		return err
	}
	if len(c.loops) > 0 {
		c.w.wakeLoops("unpause", c.loops...)
	}
	return nil
}

func (c *controls) SlotUp() (string, error)   { return c.step(1) }
func (c *controls) SlotDown() (string, error) { return c.step(-1) }

func (c *controls) step(d int) (string, error) {
	p := c.w.pool.Load()
	if p == nil {
		return "pool not ready", nil
	}
	cfgMax := c.cfgMax()
	n, changed := c.w.slots.Step(d, cfgMax)
	if !changed {
		return fmt.Sprintf("max_pods %d: at limit (1..%d)", n, slotLimit(cfgMax)), nil
	}
	p.SetMax(n)
	if d < 0 {
		if st := p.Stats(); st.Held > n {
			return fmt.Sprintf("max_pods %d, draining %d running", n, st.Held), nil
		}
	}
	return fmt.Sprintf("max_pods %d (config %d)", n, cfgMax), nil
}
