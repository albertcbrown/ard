// Package scannerdb holds one SQLite file per scanner: fingerprint to issue,
// first and last seen, suppressions and run history. Every DB owns its own
// connection pool and file; there is no ATTACH and no cross-file query, so
// scanners never share or lock each other's state.
package scannerdb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"time"

	"github.com/albertcbrown/ard/internal/state/sqlitex"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// nameRE mirrors the role-name pattern of the config package (#11); keep in
// sync. It rejects "", path separators, "..", and the controller file "_controller".
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Options configures a DB.
type Options struct {
	Now func() time.Time // nil => time.Now; used for migration timestamps
}

// DB is one scanner's database.
type DB struct {
	name string
	db   *sql.DB
}

// Path returns dir/<scanner>.db after validating the scanner name.
func Path(dir, scanner string) (string, error) {
	if !nameRE.MatchString(scanner) {
		return "", fmt.Errorf("invalid scanner name %q", scanner)
	}
	return filepath.Join(dir, scanner+".db"), nil
}

// Open opens (creating and migrating) dir/<scanner>.db.
func Open(ctx context.Context, dir, scanner string, o Options) (*DB, error) {
	p, err := Path(dir, scanner)
	if err != nil {
		return nil, fmt.Errorf("open scanner db: %w", err)
	}
	db, err := sqlitex.Open(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("open scanner db %s: %w", scanner, err)
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("scanner migrations: %w", err)
	}
	if _, err := sqlitex.Migrate(ctx, db, sub, now()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate scanner db %s: %w", scanner, err)
	}
	return &DB{name: scanner, db: db}, nil
}

// Name returns the scanner name.
func (d *DB) Name() string { return d.name }

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }

// Meta returns the value for key, or ErrNotFound.
func (d *DB) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("scanner %s meta %q: %w", d.name, key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("scanner %s read meta %q: %w", d.name, key, err)
	}
	return v, nil
}

// SetMeta sets key to value.
func (d *DB) SetMeta(ctx context.Context, key, value string) error {
	if _, err := d.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("scanner %s set meta %q: %w", d.name, key, err)
	}
	return nil
}
