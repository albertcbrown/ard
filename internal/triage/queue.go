package triage

import (
	"sort"
	"sync"
)

// QueueItem is one issue waiting for a human to add ard:approved.
type QueueItem struct {
	Issue   int
	Title   string
	Role    string
	PlanURL string
}

// ApprovalQueue is the in-memory list of issues the gate is waiting on. The
// triage loop writes it; the command center reads it. Safe for concurrent use.
type ApprovalQueue struct {
	mu    sync.Mutex
	items map[int]QueueItem
}

// Add records or refreshes an item; a repeat does not duplicate it.
func (q *ApprovalQueue) Add(it QueueItem) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.items == nil {
		q.items = map[int]QueueItem{}
	}
	q.items[it.Issue] = it
}

// Remove drops issue n.
func (q *ApprovalQueue) Remove(n int) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.items, n)
}

// Snapshot returns the items ordered by issue number.
func (q *ApprovalQueue) Snapshot() []QueueItem {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]QueueItem, 0, len(q.items))
	for _, it := range q.items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Issue < out[j].Issue })
	return out
}
