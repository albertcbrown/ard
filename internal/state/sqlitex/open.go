// Package sqlitex opens SQLite files with fixed pragmas and applies embedded,
// forward-only migrations. It uses the pure-Go modernc.org/sqlite driver.
package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite" // pure-Go driver, registers "sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Open creates path (mode 0600) if missing and opens it with WAL, a 5 s busy
// timeout, foreign keys and immediate transactions. The pool holds a single
// connection: inside a transaction every query must use the *sql.Tx and every
// *sql.Rows must be closed before the next query.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close %s: %w", path, err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Opaque: filepath.ToSlash(path), RawQuery: q.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	// Switching to WAL needs a lock that busy_timeout does not cover when
	// several handles open a new file at once, so retry on SQLITE_BUSY.
	var mode string
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode)
		if err == nil {
			break
		}
		var se *sqlite.Error
		if !errors.As(err, &se) || se.Code()&0xff != sqlite3.SQLITE_BUSY || time.Now().After(deadline) {
			db.Close()
			return nil, fmt.Errorf("set journal_mode of %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			db.Close()
			return nil, fmt.Errorf("set journal_mode of %s: %w", path, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		db.Close()
		return nil, fmt.Errorf("read foreign_keys of %s: %w", path, err)
	}
	if mode != "wal" || fk != 1 {
		db.Close()
		return nil, fmt.Errorf("open %s: pragmas not applied (journal_mode=%s foreign_keys=%d)", path, mode, fk)
	}
	return db, nil
}
