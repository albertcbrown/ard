package cli

import (
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// CommandPaths lists every command path ("ard config check") with the
// names of the flags it accepts ("--loops"), including inherited global
// flags. The docs drift check reads it.
func CommandPaths() map[string][]string {
	out := map[string][]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		var flags []string
		add := func(f *pflag.Flag) { flags = append(flags, "--"+f.Name) }
		c.LocalFlags().VisitAll(add)
		c.InheritedFlags().VisitAll(add)
		sort.Strings(flags)
		out[c.CommandPath()] = flags
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(NewRoot(&App{}))
	return out
}
