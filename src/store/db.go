// Package store is the plugin's persistence layer: SQL migrations, a
// repository over a small DB interface, retention purge and input validation.
//
// The repository is written against DB, which has the shape of the host's
// plugindb Store (Exec returns rows affected, Query returns a JSON array of
// row objects). On a till it runs over the host functions db_exec / db_query
// (ABI 2, ut-docs#3157; package hostdb). Tests use memdb (a statement-level
// double) and, where the sqlite3 command is installed, real SQLite; the
// packaged artifact is also run against the real host (scripts/realhost.sh).
//
// The host runs one statement per call and refuses BEGIN/COMMIT, so no write
// is atomic across statements. Writes are ordered so that a crash leaves
// either nothing visible or a state Reconcile repairs, and a retry is
// idempotent (see Repository). Two writes are not repaired automatically: the
// delete-then-insert of working hours and of a service's resources; a crash
// between the two leaves partial hours until the form is saved again.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// DB is the subset of the host database the repository needs. Placeholders
// are numbered (?1, ?2, ...). Times are Unix seconds (UTC), booleans 0/1.
type DB interface {
	// Exec runs one write statement and returns the rows affected.
	Exec(ctx context.Context, q string, args ...any) (int64, error)
	// Query runs one statement and returns the rows as a JSON array of
	// objects keyed by column name (or alias).
	Query(ctx context.Context, q string, args ...any) ([]byte, error)
}

type row map[string]any

func (r row) str(k string) string {
	s, _ := r[k].(string)
	return s
}

func (r row) i64(k string) int64 {
	switch v := r[k].(type) {
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			f, _ := v.Float64()
			return int64(f)
		}
		return n
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

func (r row) flag(k string) bool { return r.i64(k) != 0 }

// query runs q and decodes the rows, keeping integers exact (UseNumber).
func query(ctx context.Context, db DB, q string, args ...any) ([]row, error) {
	b, err := db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var rows []row
	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("store: decode rows: %w", err)
	}
	return rows, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
