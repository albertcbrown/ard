package digest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/state"
)

// Data is what one digest reports. Day is the UTC day covered (2006-01-02).
type Data struct {
	Day   string
	Queue map[string]int
	Jobs  map[state.RoleKey]state.JobCounts
	Spend map[state.RoleKey]state.Spend
	Total state.Spend
}

func (d Data) counts() (jobs, failed int) {
	for _, c := range d.Jobs {
		jobs += c.Done + c.Failed
		failed += c.Failed
	}
	return
}

var safeName = regexp.MustCompile(`^[a-z0-9._/:-]+$`)

func safe(s string) string {
	if safeName.MatchString(s) {
		return s
	}
	return "?"
}

// Render is the digest comment. It holds only numbers, the day and label and
// role names; no agent text, no issue titles and no "@".
func Render(d Data) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- ard:digest day=%s -->\n", safe(d.Day))
	fmt.Fprintf(&b, "**ard · daily digest for %s (UTC)**\n\n", safe(d.Day))

	b.WriteString("**Queue depth** (open issues by label)\n\n")
	if len(d.Queue) == 0 {
		b.WriteString("No open issue carries an `ard:` label.\n")
	} else {
		b.WriteString("| Label | Issues |\n|---|---|\n")
		names := make([]string, 0, len(d.Queue))
		for n := range d.Queue {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "| `%s` | %d |\n", safe(n), d.Queue[n])
		}
	}

	jobs, failed := d.counts()
	fmt.Fprintf(&b, "\n**Jobs run:** %d\n\n**Failures:** %d\n", jobs, failed)
	keys := make([]state.RoleKey, 0, len(d.Jobs))
	for k, c := range d.Jobs {
		if c.Failed > 0 {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Stage != keys[j].Stage {
			return keys[i].Stage < keys[j].Stage
		}
		return keys[i].Role < keys[j].Role
	})
	if len(keys) > 0 {
		b.WriteString("\n| Role | Failed |\n|---|---|\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s/%s | %d |\n", safe(k.Stage), safe(k.Role), d.Jobs[k].Failed)
		}
	}

	fmt.Fprintf(&b, "\n**Spend:** $%.2f", d.Total.USD)
	if d.Total.Unpriced > 0 {
		fmt.Fprintf(&b, " (%d steps reported no cost and count as $0)", d.Total.Unpriced)
	}
	b.WriteString("\n\nOn a subscription the dollar figure is an estimate.\n")
	return b.String()
}
