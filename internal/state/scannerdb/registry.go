package scannerdb

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Registry opens each scanner file lazily and never shares a handle between
// scanners.
type Registry struct {
	dir    string
	opts   Options
	mu     sync.Mutex
	dbs    map[string]*DB
	closed bool
}

// NewRegistry returns a Registry over dir.
func NewRegistry(dir string, o Options) *Registry {
	return &Registry{dir: dir, opts: o, dbs: map[string]*DB{}}
}

// Get returns the DB for scanner, opening it on first use.
func (r *Registry) Get(ctx context.Context, scanner string) (*DB, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errors.New("scanner registry is closed")
	}
	if d, ok := r.dbs[scanner]; ok {
		return d, nil
	}
	d, err := Open(ctx, r.dir, scanner, r.opts)
	if err != nil {
		return nil, err
	}
	r.dbs[scanner] = d
	return d, nil
}

// Close closes every open DB. It is idempotent.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var errs []error
	for name, d := range r.dbs {
		if err := d.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close scanner db %s: %w", name, err))
		}
	}
	r.dbs = nil
	return errors.Join(errs...)
}
