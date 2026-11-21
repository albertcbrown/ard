package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ControllerFile is the controller database file name. The leading underscore
// cannot occur in a role name, so it never collides with a scanner file.
const ControllerFile = "_controller.db"

var (
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("a running job already exists for this stage, role and issue")
	ErrNotRunning = errors.New("job is not running")
)

// Options configures a Store.
type Options struct {
	Now func() time.Time // nil => time.Now; stored as UTC seconds
}

// Store is the controller database.
type Store struct {
	db  *sql.DB
	now func() time.Time
	mu  sync.Mutex
	ent *ulid.MonotonicEntropy
}

// Open opens dir/_controller.db (creating dir with 0700) and migrates it.
func Open(ctx context.Context, dir string, o Options) (*Store, error) {
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	db, err := sqlitex.Open(ctx, filepath.Join(dir, ControllerFile))
	if err != nil {
		return nil, fmt.Errorf("open controller db: %w", err)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("controller migrations: %w", err)
	}
	if _, err := sqlitex.Migrate(ctx, db, sub, now()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate controller db: %w", err)
	}
	return &Store{db: db, now: now, ent: ulid.Monotonic(rand.Reader, 0)}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) newID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := ulid.New(ulid.Timestamp(s.now()), s.ent)
	if err != nil {
		return "", fmt.Errorf("mint job id: %w", err)
	}
	return id.String(), nil
}

// Meta returns the value for key, or ErrNotFound.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("meta %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("read meta %q: %w", key, err)
	}
	return v, nil
}

// SetMeta sets key to value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
