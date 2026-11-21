package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrSchemaTooNew means the database was written by a newer binary.
var ErrSchemaTooNew = errors.New("database schema is newer than this binary")

// ErrBadMigration means the embedded migration set is malformed or no longer
// matches what was applied.
var ErrBadMigration = errors.New("bad migration set")

var (
	fileRE   = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)
	forbidRE = regexp.MustCompile(`(?im)^\s*(BEGIN|COMMIT|VACUUM)\b|PRAGMA\s+journal_mode`)
)

type migration struct {
	version int
	name    string
	sql     string
}

func load(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: read dir: %v", ErrBadMigration, err)
	}
	var ms []migration
	for _, e := range entries {
		m := fileRE.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf("%w: unexpected file %q", ErrBadMigration, e.Name())
		}
		v, _ := strconv.Atoi(m[1])
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if forbidRE.Match(b) {
			return nil, fmt.Errorf("%w: %s contains BEGIN, COMMIT, VACUUM or PRAGMA journal_mode", ErrBadMigration, e.Name())
		}
		ms = append(ms, migration{v, strings.TrimSuffix(e.Name(), ".sql"), string(b)})
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("%w: no migrations", ErrBadMigration)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("%w: expected version %04d, found %04d", ErrBadMigration, i+1, m.version)
		}
	}
	return ms, nil
}

// Migrate applies every migration in fsys (root dir) whose version exceeds the
// recorded one, each in its own transaction together with its schema_version
// row. It returns the resulting version.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, now time.Time) (int, error) {
	ms, err := load(fsys)
	if err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version(
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return 0, fmt.Errorf("create schema_version: %w", err)
	}
	applied := map[int]string{}
	rows, err := db.QueryContext(ctx, `SELECT version, name FROM schema_version`)
	if err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	cur := 0
	for rows.Next() {
		var v int
		var n string
		if err := rows.Scan(&v, &n); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan schema_version: %w", err)
		}
		applied[v] = n
		if v > cur {
			cur = v
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	rows.Close()
	if cur > len(ms) {
		return cur, fmt.Errorf("%w: database at %d, binary at %d", ErrSchemaTooNew, cur, len(ms))
	}
	for v, n := range applied {
		if ms[v-1].name != n {
			return cur, fmt.Errorf("%w: version %d recorded as %q, embedded as %q", ErrBadMigration, v, n, ms[v-1].name)
		}
	}
	for _, m := range ms[cur:] {
		if err := apply(ctx, db, m, now); err != nil {
			return cur, err
		}
		cur = m.version
	}
	return cur, nil
}

func apply(ctx context.Context, db *sql.DB, m migration, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version, name, applied_at) VALUES(?,?,?)`,
		m.version, m.name, now.Unix()); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}
