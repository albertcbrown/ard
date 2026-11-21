package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func open(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	ctx := tctx(t)
	db, err := Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return ctx, db
}

func fsOf(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func tableExists(t *testing.T, ctx context.Context, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestMigrate(t *testing.T) {
	v1 := map[string]string{"0001_a.sql": `CREATE TABLE a(x); INSERT INTO a VALUES(1);`}
	v2 := map[string]string{"0001_a.sql": v1["0001_a.sql"], "0002_b.sql": `CREATE TABLE b(y);`}
	t0 := time.Unix(1000, 0)

	t.Run("fresh to latest and rerun is a no-op", func(t *testing.T) {
		ctx, db := open(t)
		if v, err := Migrate(ctx, db, fsOf(v2), t0); err != nil || v != 2 {
			t.Fatalf("v=%d err=%v", v, err)
		}
		if v, err := Migrate(ctx, db, fsOf(v2), t0.Add(time.Hour)); err != nil || v != 2 {
			t.Fatalf("rerun v=%d err=%v", v, err)
		}
		var at int64
		if err := db.QueryRowContext(ctx, `SELECT applied_at FROM schema_version WHERE version = 1`).Scan(&at); err != nil || at != 1000 {
			t.Errorf("applied_at = %d err=%v", at, err)
		}
	})
	t.Run("v1 upgrades to v2", func(t *testing.T) {
		ctx, db := open(t)
		if _, err := Migrate(ctx, db, fsOf(v1), t0); err != nil {
			t.Fatal(err)
		}
		if v, err := Migrate(ctx, db, fsOf(v2), t0); err != nil || v != 2 || !tableExists(t, ctx, db, "b") {
			t.Fatalf("v=%d err=%v", v, err)
		}
	})
	t.Run("failing file rolls back", func(t *testing.T) {
		ctx, db := open(t)
		bad := map[string]string{"0001_a.sql": v1["0001_a.sql"], "0002_b.sql": `CREATE TABLE b(y); INSERT INTO nope VALUES(1);`}
		v, err := Migrate(ctx, db, fsOf(bad), t0)
		if err == nil || v != 1 {
			t.Fatalf("v=%d err=%v", v, err)
		}
		if tableExists(t, ctx, db, "b") {
			t.Error("table b survived a failed migration")
		}
	})
	t.Run("schema too new", func(t *testing.T) {
		ctx, db := open(t)
		v3 := map[string]string{"0001_a.sql": "CREATE TABLE a(x);", "0002_b.sql": "CREATE TABLE b(y);", "0003_c.sql": "CREATE TABLE c(z);"}
		if _, err := Migrate(ctx, db, fsOf(v3), t0); err != nil {
			t.Fatal(err)
		}
		if _, err := Migrate(ctx, db, fsOf(v2), t0); !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("err = %v", err)
		}
		if !tableExists(t, ctx, db, "c") {
			t.Error("database was touched")
		}
	})
	t.Run("bad sets", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"gap":        {"0001_a.sql": "SELECT 1;", "0003_c.sql": "SELECT 1;"},
			"bad name":   {"0001_a.sql": "SELECT 1;", "readme.txt": "x"},
			"empty":      {},
			"begin":      {"0001_a.sql": "BEGIN;\nCREATE TABLE a(x);\nCOMMIT;"},
			"journal":    {"0001_a.sql": "PRAGMA journal_mode=DELETE;"},
			"not number": {"1_a.sql": "SELECT 1;"},
		} {
			ctx, db := open(t)
			if _, err := Migrate(ctx, db, fsOf(files), t0); !errors.Is(err, ErrBadMigration) {
				t.Errorf("%s: err = %v", name, err)
			}
		}
	})
	t.Run("name mismatch", func(t *testing.T) {
		ctx, db := open(t)
		if _, err := Migrate(ctx, db, fsOf(v1), t0); err != nil {
			t.Fatal(err)
		}
		renamed := map[string]string{"0001_z.sql": v1["0001_a.sql"]}
		if _, err := Migrate(ctx, db, fsOf(renamed), t0); !errors.Is(err, ErrBadMigration) {
			t.Fatalf("err = %v", err)
		}
	})
}
