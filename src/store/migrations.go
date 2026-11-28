package store

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration has the shape of the host plugindb.Migration: versions run 1..N
// without gaps, and an applied migration is never edited (the host pins the
// checksum of the exact text).
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Checksum is the hex SHA-256 of the migration text.
func (m Migration) Checksum() string {
	sum := sha256.Sum256([]byte(m.SQL))
	return hex.EncodeToString(sum[:])
}

var migrationName = regexp.MustCompile(`^([0-9]{4})_([a-z0-9][a-z0-9_-]{0,63})\.sql$`)

// Migrations returns the embedded migrations in version order.
func Migrations() ([]Migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("store: %q is not a NNNN_name.sql migration", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		v, _ := strconv.Atoi(m[1])
		out = append(out, Migration{Version: v, Name: m[2], SQL: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("store: migrations must be numbered 1..N, got %d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}
