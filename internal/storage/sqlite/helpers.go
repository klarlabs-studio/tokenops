package sqlite

import (
	"database/sql"
	"time"
)

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64IfNonZero(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullFloat64IfNonZero(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func timeFromNS(ns int64) time.Time {
	return time.Unix(0, ns).UTC()
}

func nowUnixNano() int64 {
	return time.Now().UTC().UnixNano()
}
