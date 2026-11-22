package pool

import "time"

// Slot is a granted reservation, held from Offer until Release.
type Slot struct {
	pool      *Pool
	cand      Candidate
	jobID     string
	grantedAt time.Time
	released  bool
	adopted   bool
}

// Key returns the job key.
func (s *Slot) Key() string { return s.cand.Key() }

// Candidate returns the candidate the slot was granted to.
func (s *Slot) Candidate() Candidate { return s.cand }

// Adopted reports whether the slot was built from a job that was already
// running when the pool was created (restart recovery), so no goroutine of
// this process drives it. The Reaper closes such a job out itself.
func (s *Slot) Adopted() bool { return s.adopted }

// JobID returns the id set by SetJobID, or "".
func (s *Slot) JobID() string {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	return s.jobID
}

// SetJobID records the job's ULID once the loop has minted it. It is used
// for logs and Stats only.
func (s *Slot) SetJobID(id string) {
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	s.jobID = id
}

// Release frees the slot. It returns true on the first call and false after,
// so a stale handle never frees a slot re-granted under the same key. Safe
// from any goroutine.
func (s *Slot) Release() bool {
	p := s.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.released {
		return false
	}
	s.released = true
	if p.held[s.Key()] == s {
		delete(p.held, s.Key())
	}
	p.log.Debug("pool.release", "key", s.Key(), "class", s.cand.Class.String(),
		"role", s.cand.Role, "number", s.cand.Number, "job_id", s.jobID)
	return true
}
