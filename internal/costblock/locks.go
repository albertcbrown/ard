package costblock

import "sync"

// Locks serialises body read-modify-writes per issue within the process, so
// the scanner and the costs loop never interleave on one body. A nil *Locks is
// a no-op.
type Locks struct {
	mu sync.Mutex
	m  map[int]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

// Lock blocks until issue n is free and returns the function that frees it.
func (l *Locks) Lock(n int) (unlock func()) {
	if l == nil {
		return func() {}
	}
	l.mu.Lock()
	if l.m == nil {
		l.m = map[int]*entry{}
	}
	e := l.m[n]
	if e == nil {
		e = &entry{}
		l.m[n] = e
	}
	e.refs++
	l.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, n)
		}
		l.mu.Unlock()
	}
}
