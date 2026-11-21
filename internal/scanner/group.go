package scanner

import (
	"sort"
)

// Item is a finding queued for filing; RegressionOf links the old issue.
type Item struct {
	F            Finding
	RegressionOf int
}

// Joinable is an open, unclaimed issue a new finding may join.
type Joinable struct {
	Number int
	Group  string
	Lines  int // checklist lines already on it, checked ones included
}

// GroupOp is one filing action: append Items to issue Join, or, when Join is
// 0, create an issue for them in Group.
type GroupOp struct {
	Join  int
	Group string
	Items []Item
}

type unit struct {
	group string
	items []Item
	best  int
}

func lessItem(a, b Item) bool {
	if ra, rb := a.F.Severity.Rank(), b.F.Severity.Rank(); ra != rb {
		return ra > rb
	}
	return a.F.Fingerprint < b.F.Fingerprint
}

// PlanGroups decides where items go, most severe first so a tight budget
// files what matters. An empty group always gets its own issue. Otherwise, per
// group key, new findings fill joinable issues of that group (lowest number
// first; checked lines count against groupSize, so a checklist cannot grow
// without bound) and the rest are chunked into new issues of at most
// groupSize. A regression never joins: it files a new issue, which may also
// hold new findings of the same group. budget bounds the number of new
// issues; joins are free. It returns the ops and the number of items left
// unfiled for lack of budget, which the next run offers again.
func PlanGroups(items []Item, joinable []Joinable, groupSize, budget int) (ops []GroupOp, overBudget int) {
	if groupSize < 1 {
		groupSize = 1
	}
	byGroup := map[string][]Item{}
	var units []unit
	for _, it := range items {
		if it.F.Group == "" {
			units = append(units, unit{items: []Item{it}, best: it.F.Severity.Rank()})
			continue
		}
		byGroup[it.F.Group] = append(byGroup[it.F.Group], it)
	}
	for g, its := range byGroup {
		sort.Slice(its, func(i, j int) bool { return lessItem(its[i], its[j]) })
		units = append(units, unit{group: g, items: its, best: its[0].F.Severity.Rank()})
	}
	sort.Slice(units, func(i, j int) bool {
		a, b := units[i], units[j]
		switch {
		case a.best != b.best:
			return a.best > b.best
		case a.group != b.group:
			return a.group < b.group
		}
		return a.items[0].F.Fingerprint < b.items[0].F.Fingerprint
	})

	room := map[int]int{}
	for _, j := range joinable {
		room[j.Number] = groupSize - j.Lines
	}
	byGroupJoin := map[string][]Joinable{}
	for _, j := range joinable {
		byGroupJoin[j.Group] = append(byGroupJoin[j.Group], j)
	}
	for _, js := range byGroupJoin {
		sort.Slice(js, func(a, b int) bool { return js[a].Number < js[b].Number })
	}

	for _, u := range units {
		rest := u.items
		if u.group != "" {
			var fresh, regress []Item
			for _, it := range u.items {
				if it.RegressionOf > 0 {
					regress = append(regress, it)
				} else {
					fresh = append(fresh, it)
				}
			}
			for _, j := range byGroupJoin[u.group] {
				if len(fresh) == 0 {
					break
				}
				n := min(room[j.Number], len(fresh))
				if n <= 0 {
					continue
				}
				ops = append(ops, GroupOp{Join: j.Number, Group: u.group, Items: fresh[:n]})
				room[j.Number] -= n
				fresh = fresh[n:]
			}
			rest = append(regress, fresh...)
			sort.Slice(rest, func(i, j int) bool { return lessItem(rest[i], rest[j]) })
		}
		for len(rest) > 0 {
			n := min(groupSize, len(rest))
			if budget <= 0 {
				overBudget += len(rest)
				break
			}
			budget--
			ops = append(ops, GroupOp{Group: u.group, Items: rest[:n]})
			rest = rest[n:]
		}
	}
	return ops, overBudget
}
