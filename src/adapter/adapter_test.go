package adapter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/adapter"
	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

type clk struct{ t time.Time }

func (c *clk) Now() time.Time { return c.t }

type cat map[string]adapter.Item

func (c cat) Items(skus []string) (map[string]adapter.Item, error) {
	out := map[string]adapter.Item{}
	for _, s := range skus {
		if it, ok := c[s]; ok {
			out[s] = it
		}
	}
	return out, nil
}

type rig struct {
	t    *testing.T
	db   *memdb.DB
	repo *store.Repository
	st   *adapter.Store
	d    calendar.Deps
	clk  *clk
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db := memdb.New()
	c := &clk{t: time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC)}
	n := 0
	ids := func() string { n++; return fmt.Sprintf("id-%d", n) }
	repo := store.New(db, c, ids, "till-main")
	ctx := context.Background()
	hours := map[time.Weekday][]engine.Window{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		hours[d] = []engine.Window{{StartMin: 9 * 60, EndMin: 17 * 60}}
	}
	for _, id := range []string{"st1", "st2"} {
		if err := repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: id, Kind: "staff", Active: true}, DisplayName: strings.ToUpper(id)}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SetWorkingHours(ctx, id, hours); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, BufferMin: 5, All: true, Active: true}); err != nil {
		t.Fatal(err)
	}
	st := &adapter.Store{Repo: repo, OriginTill: "till-main", Catalogue: cat{"cut": {Name: "Cut", Price: money.FromMinor(2500), Active: true}}}
	return &rig{t: t, db: db, repo: repo, st: st, clk: c, d: calendar.Deps{Store: st, IDs: ids, Route: "/plugin/appointments"}}
}

func (r *rig) ask(action string, op *calendar.Operator, form map[string]any) calendar.Answer {
	r.t.Helper()
	ans, err := r.d.Handle(calendar.Ask{View: calendar.ViewDay, Action: action, Form: form, Locale: "en", Operator: op, Now: r.clk.t,
		Params: map[string]string{"date": "2026-01-14"}})
	if err != nil {
		r.t.Fatalf("%s: %v", action, err)
	}
	return ans
}

func (r *rig) doc(a calendar.Answer) string {
	b, _ := json.Marshal(a.Document)
	return string(b)
}

var mgr = &calendar.Operator{UserID: "u-mgr", Role: "manager"}

func (r *rig) bookAt(h int, name string) store.Booking {
	r.t.Helper()
	start := time.Date(2026, 1, 14, h, 0, 0, 0, time.UTC)
	r.ask("create", mgr, map[string]any{"staff": "st1", "service": "cut", "date": "2026-01-14", "first_name": name, "time": start.Format(time.RFC3339)})
	rows, err := r.repo.Current(context.Background(), start.Add(-time.Hour), start.Add(time.Hour), "st1")
	if err != nil || len(rows) != 1 {
		r.t.Fatalf("booked rows %d %v", len(rows), err)
	}
	return rows[0]
}

func TestBookMoveAndTransitionThroughTheRepository(t *testing.T) {
	r := newRig(t)
	b := r.bookAt(10, "Ana")
	if b.Currency != "GBP" || b.Lines[0].Name != "Cut" || b.Lines[0].Amount.Minor() != 2500 {
		t.Fatalf("booking = %+v", b)
	}
	// move: new row supersedes the old one, which is "not found" afterwards
	newStart := time.Date(2026, 1, 14, 12, 0, 0, 0, time.UTC)
	r.ask("domove_"+tok(b.ID), mgr, map[string]any{"staff": "st1", "service": "cut", "date": "2026-01-14", "first_name": "Ana", "time": newStart.Format(time.RFC3339)})
	cur, _ := r.repo.Current(context.Background(), newStart.Add(-6*time.Hour), newStart.Add(2*time.Hour), "")
	if len(cur) != 1 || !cur[0].Start.Equal(newStart) || cur[0].SupersedesID != b.ID {
		t.Fatalf("after move: %+v", cur)
	}
	if _, ok, err := r.st.Booking(b.ID); ok || err != nil {
		t.Fatalf("superseded row visible: %v %v", ok, err)
	}
	// repeating the same move post is refused, not forked (N13)
	r.ask("domove_"+tok(b.ID), mgr, map[string]any{"staff": "st1", "service": "cut", "date": "2026-01-14", "first_name": "Ana", "time": time.Date(2026, 1, 14, 14, 0, 0, 0, time.UTC).Format(time.RFC3339)})
	if cur, _ := r.repo.Current(context.Background(), time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), ""); len(cur) != 1 {
		t.Fatalf("a repeated move forked the chain: %d current rows", len(cur))
	}
	// a state change through the calendar reaches the repository with its event
	head := cur0(t, r)
	r.ask("do_checkin_"+tok(head.ID), mgr, nil)
	g, _ := r.repo.Get(context.Background(), head.ID)
	if g.State != engine.CheckedIn {
		t.Fatalf("state %s", g.State)
	}
	if evs, _ := r.repo.Events(context.Background(), head.ID); len(evs) < 2 || evs[len(evs)-1].ToState != engine.CheckedIn {
		t.Fatalf("events %+v", evs)
	}
}

func cur0(t *testing.T, r *rig) store.Booking {
	t.Helper()
	cur, err := r.repo.Current(context.Background(), time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "")
	if err != nil || len(cur) != 1 {
		t.Fatalf("current %d %v", len(cur), err)
	}
	return cur[0]
}

func tok(id string) string { return strings.ReplaceAll(id, "-", "_") }

func TestWalkInQueueSeatAndDrop(t *testing.T) {
	r := newRig(t)
	r.clk.t = time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	cashier := &calendar.Operator{UserID: "u-c", Role: "cashier"}
	r.ask("queue_add", cashier, map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"})
	r.ask("queue_add", cashier, map[string]any{"first_name": "Kim", "service": "cut", "staff": "any"})
	q, err := r.st.Queue()
	if err != nil || len(q) != 2 || q[0].FirstName != "Sam" {
		t.Fatalf("queue %+v %v", q, err)
	}
	r.ask("seat_"+tok(q[0].ID), cashier, nil)
	r.ask("drop_"+tok(q[1].ID), cashier, nil)
	if q, _ := r.st.Queue(); len(q) != 0 {
		t.Fatalf("queue not empty: %+v", q)
	}
	cur := cur0(t, r)
	if !cur.WalkIn || cur.State != engine.CheckedIn || cur.FirstName != "Sam" {
		t.Fatalf("seated %+v", cur)
	}
	// seating twice (stale post) does nothing
	r.ask("seat_"+tok(cur.ID), cashier, nil)
	if cur2 := cur0(t, r); cur2.ID != cur.ID {
		t.Fatal("seated twice")
	}
}

func TestServiceGoneFromCatalogueIsNotBookable(t *testing.T) {
	r := newRig(t)
	r.st.Catalogue = cat{}
	svcs, err := r.st.Services()
	if err != nil || len(svcs) != 1 || svcs[0].Active {
		t.Fatalf("services %+v %v", svcs, err)
	}
}
