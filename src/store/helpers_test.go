package store_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

type clock struct{ t time.Time }

func (c *clock) Now() time.Time { return c.t }

func counter(prefix string) engine.IDGen {
	n := 0
	return func() string { n++; return fmt.Sprintf("%s%d", prefix, n) }
}

// d(day, h, m) is 2026-01 day at h:m UTC (London is GMT in January).
func at(day, h, m int) time.Time { return time.Date(2026, time.January, day, h, m, 0, 0, time.UTC) }

type env struct {
	repo *store.Repository
	clk  *clock
	db   store.DB
	dump func() string
}

// backend builds a fresh database with all migrations applied.
type backend struct {
	name string
	make func(t *testing.T) (store.DB, func() string)
}

func backends() []backend {
	return []backend{
		{"memdb", func(t *testing.T) (store.DB, func() string) { d := memdb.New(); return d, d.Dump }},
		{"sqlite", func(t *testing.T) (store.DB, func() string) {
			c := newCLIDB(t)
			ms, err := store.Migrations()
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range ms {
				if err := c.script(m.SQL); err != nil {
					t.Fatalf("migration %d: %v", m.Version, err)
				}
			}
			return c, func() string {
				out, err := c.run(".dump\n")
				if err != nil {
					t.Fatal(err)
				}
				return string(out)
			}
		}},
	}
}

func forEachBackend(t *testing.T, fn func(t *testing.T, e *env)) {
	forEachBackendWith(t, nil, fn)
}

// hostMaxResultBytes mirrors the host's plugindb MaxResultBytes (1 MiB): a
// query whose JSON result is larger fails with ErrResultTooLarge.
const hostMaxResultBytes = 1 << 20

// capDB enforces a result cap like the host's plugindb does.
type capDB struct {
	store.DB
	max int
}

func (c capDB) Query(ctx context.Context, q string, args ...any) ([]byte, error) {
	b, err := c.DB.Query(ctx, q, args...)
	if err == nil && len(b) > c.max {
		return nil, fmt.Errorf("plugindb: result too large (%d > %d bytes)", len(b), c.max)
	}
	return b, err
}

func capped(max int) func(store.DB) store.DB {
	return func(d store.DB) store.DB { return capDB{DB: d, max: max} }
}

func forEachBackendWith(t *testing.T, wrap func(store.DB) store.DB, fn func(t *testing.T, e *env)) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			db, dump := b.make(t)
			if wrap != nil {
				db = wrap(db)
			}
			clk := &clock{t: at(14, 8, 0)}
			fn(t, &env{repo: store.New(db, clk, counter("id"), "till1"), clk: clk, db: db, dump: dump})
		})
	}
}

func line(sku string, dur, buf int) engine.Line {
	return engine.Line{SKU: sku, Name: "Cut", Qty: 1, Amount: money.FromMinor(2500), DurationMin: dur, BufferMin: buf}
}

func draft(res string, start time.Time, dur, buf int) engine.Appointment {
	return engine.Appointment{ResourceID: res, Start: start, End: start.Add(time.Duration(dur) * time.Minute), BufferMin: buf,
		Lines: []engine.Line{line("cut", dur, buf)}}
}

func mustCreate(t *testing.T, e *env, res string, start time.Time, name string) store.Booking {
	t.Helper()
	b, err := e.repo.Create(context.Background(), store.NewBooking{Appointment: draft(res, start, 30, 0), FirstName: name, ActorUserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func reflectType(v any) reflect.Type { return reflect.TypeOf(v) }
