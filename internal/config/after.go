package config

import (
	"sort"
	"strings"
)

// checkAfter reports triage roles whose after list names the role itself or
// closes a cycle, which would hold the roles forever. Deterministic: roles by
// name, dependencies in config order. A cycle is reported once, on its
// lexically smallest member.
func checkAfter(roles []Role, cfgPath string) Errors {
	deps := map[string][]string{}
	var names []string
	for _, r := range roles {
		if r.Kind == KindTriage {
			deps[r.Name] = r.After
			names = append(names, r.Name)
		}
	}
	sort.Strings(names)
	var errs Errors
	for _, start := range names {
		key := overrideKeyRef("triage/"+start) + ".after"
		self := false
		for _, d := range deps[start] {
			self = self || d == start
		}
		if self {
			errs = append(errs, &Error{File: cfgPath, Key: key, Msg: "a role cannot be after itself"})
		}
		// Look for a path back to start through roles that sort after it, so
		// start is the smallest member of any cycle reported here.
		seen := map[string]bool{}
		var walk func(cur string, path []string) []string
		walk = func(cur string, path []string) []string {
			for _, d := range deps[cur] {
				if d == start && cur != start {
					return append(path, start)
				}
				if d <= start || seen[d] {
					continue
				}
				seen[d] = true
				if p := walk(d, append(path, d)); p != nil {
					return p
				}
			}
			return nil
		}
		if p := walk(start, []string{start}); p != nil {
			errs = append(errs, &Error{File: cfgPath, Key: key, Msg: "cycle: " + strings.Join(p, " -> ")})
		}
	}
	return errs
}
