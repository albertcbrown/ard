package closeout

import (
	"errors"
	"fmt"
	"strings"
)

// mergeOwnSection returns a committer merge func that puts the job's own
// plan.md section (own, without "## ") into the copy at the latest head and
// keeps every other part of that copy, so a section another role committed
// after the job's base is not reverted. It fails closed when the head copy
// is ambiguous.
func mergeOwnSection(own string) func(cur []byte, exists bool, mine []byte) ([]byte, error) {
	return func(cur []byte, exists bool, mine []byte) ([]byte, error) {
		if !exists {
			return mine, nil
		}
		var mineSec string
		for _, s := range parsePlan(mine).sections {
			if s.heading == own {
				mineSec = s.text
				break
			}
		}
		c := parsePlan(cur)
		n := 0
		for _, s := range c.sections {
			if s.heading == own {
				n++
				if hasSetext(s.text) {
					return nil, errors.New("heading-like line in own section on main")
				}
			}
		}
		if n > 1 {
			return nil, fmt.Errorf("section ## %s appears twice on main", clip(own, 80))
		}
		var b strings.Builder
		emit := func(t string) {
			b.WriteString(t)
			if t != "" && !strings.HasSuffix(t, "\n") && !strings.HasSuffix(t, "\r") {
				b.WriteString("\n")
			}
		}
		emit(c.pre)
		for _, s := range c.sections {
			switch {
			case s.heading != own:
				emit(s.text)
			case mineSec != "":
				emit(mineSec)
				mineSec = ""
			}
		}
		if mineSec != "" {
			emit(mineSec)
		}
		return []byte(b.String()), nil
	}
}
