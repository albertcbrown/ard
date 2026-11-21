package sqlitex

import (
	"database/sql"
	"time"
)

// ToUnix stores t as UTC seconds. The zero time is NULL.
func ToUnix(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}

// FromUnix is the inverse of ToUnix: NULL is the zero time.
func FromUnix(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.Unix(n.Int64, 0).UTC()
}
