package sqlitex

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestOpenPragmasAndMode(t *testing.T) {
	ctx := tctx(t)
	path := filepath.Join(t.TempDir(), "sub", "x.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st := mustStat(t, path)
	if st != 0o600 {
		t.Errorf("mode = %o, want 600", st)
	}
	for _, tc := range []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"}, {"foreign_keys", "1"}, {"busy_timeout", "5000"}, {"synchronous", "1"},
	} {
		var got string
		if err := db.QueryRowContext(ctx, "PRAGMA "+tc.pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s = %s, want %s", tc.pragma, got, tc.want)
		}
	}
}

func TestTimeRoundTrip(t *testing.T) {
	if ToUnix(time.Time{}).Valid || !FromUnix(ToUnix(time.Time{})).IsZero() {
		t.Error("zero time must be NULL")
	}
	in := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	out := FromUnix(ToUnix(in))
	if !out.Equal(in) || out.Location() != time.UTC {
		t.Errorf("got %v", out)
	}
}
