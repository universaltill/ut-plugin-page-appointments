// Package hostdb implements store.DB on the till's plugin-owned database: the
// db_exec and db_query host functions of ABI 2 (ADR-0121 amendment 2026-11-28,
// reference/plugin-host-functions.md "Own database"), through the Go guest SDK.
//
// The host picks the file from the plugin id, so there is no path or id here.
// The SDK adds the host's reason text (db_error) to a failed call, which is
// where a constraint violation says "UNIQUE constraint failed" (store reads it).
// One statement per call; the host refuses BEGIN/COMMIT, so nothing in this
// package (or in store) relies on a transaction.
package hostdb

import (
	"context"
	"errors"

	"github.com/universaltill/universal-till/sdk/plugin"
)

// DB is store.DB over the host functions.
type DB struct{}

// New returns the host database.
func New() DB { return DB{} }

// Exec runs one write statement and returns the rows affected.
func (DB) Exec(ctx context.Context, q string, args ...any) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := plugin.DBExec(q, args...)
	return n, err
}

// Query runs one statement and returns a JSON array of row objects.
func (DB) Query(ctx context.Context, q string, args ...any) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := plugin.DBQuery(q, args...)
	return b, err
}

// Retryable reports whether the host said "busy" or "deadline": the same call
// may succeed later. A write is never retried by this package.
func Retryable(err error) bool {
	return errors.Is(err, plugin.ErrBusy) || errors.Is(err, plugin.ErrInternal)
}
