//go:build !wasip1

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/adapter"
	"github.com/universaltill/ut-plugin-page-appointments/src/app"
	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

type cat struct{}

func (cat) Items(skus []string) (map[string]adapter.Item, error) {
	return map[string]adapter.Item{"cut": {Name: "Cut", Price: money.FromMinor(2500), Active: true}}, nil
}

type rec struct{}

func (rec) Paid(string) (bool, error)           { return false, nil }
func (rec) Recent() ([]calendar.Receipt, error) { return nil, nil }

type pub struct{ n int }

func (p *pub) Publish(string, []byte) int { p.n++; return 0 }

type rig struct {
	db  *memdb.DB
	now time.Time
	h   plugin.Handlers
	n   int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{db: memdb.New(), now: time.Date(2026, 1, 14, 9, 0, 0, 0, time.UTC)}
	d := app.Deps{DB: r.db, Catalogue: cat{}, Receipts: rec{}, Pub: &pub{},
		ShopCurrency: func() (string, error) { return "EUR", nil },
		IDs:          func() string { r.n++; return fmt.Sprintf("id-%d", r.n) }}
	r.h = app.Handlers(d)
	return r
}

func (r *rig) send(t *testing.T, typ string, payload any) string {
	t.Helper()
	pl, _ := json.Marshal(payload)
	ev, _ := json.Marshal(map[string]any{"id": "e1", "type": typ, "timestamp": r.now.Format(time.RFC3339), "payload": json.RawMessage(pl)})
	out, err := plugin.Dispatch(r.h, ev, "")
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return string(out)
}

func TestEventsRouteToCalendarAdminAndTick(t *testing.T) {
	r := newRig(t)
	repo := store.New(r.db, nil, nil, "main")
	ctx := context.Background()
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "st1", Kind: "staff", Active: true}, DisplayName: "Ana"})
	_ = repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, All: true, Active: true})

	// calendar view: first run seeds the till's currency, draws the day
	if out := r.send(t, "ui.view.ask", map[string]any{"view": "appointments.day", "locale": "en"}); !strings.Contains(out, "Ana") {
		t.Fatalf("calendar: %s", out)
	}
	if set, _ := repo.Settings(ctx); set.Currency != "EUR" {
		t.Fatalf("currency not seeded from the till: %q", set.Currency)
	}
	// an admin view
	if out := r.send(t, "ui.view.ask", map[string]any{"view": "appointments.settings", "locale": "en"}); !strings.Contains(out, "document") {
		t.Fatalf("admin: %s", out)
	}
	// a calendar action: queue a walk-in
	out := r.send(t, "ui.action.ask", map[string]any{"view": "appointments.day", "action": "queue_add", "locale": "en",
		"form": map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"}})
	if !strings.Contains(out, "Sam added to the queue") {
		t.Fatalf("queue_add: %s", out)
	}
	// the tick purges yesterday's queue: move the clock a day on
	r.now = r.now.Add(26 * time.Hour)
	if out := r.send(t, app.TickEvent, map[string]any{}); out != "" {
		t.Fatalf("tick answered %q", out)
	}
	if q, _ := repo.Queue(ctx, time.Unix(0, 0)); len(q) != 0 {
		t.Fatalf("queue survived the tick: %+v", q)
	}
	// an event nobody asked for is ignored
	if out := r.send(t, "sale.completed", map[string]any{}); out != "" {
		t.Fatalf("unexpected answer %q", out)
	}
}

func TestAdminIsClosedToCashiers(t *testing.T) {
	r := newRig(t)
	out := r.send(t, "ui.view.ask", map[string]any{"view": "appointments.settings", "locale": "en",
		"operator": map[string]any{"user_id": "u1", "role": "cashier"}})
	if !strings.Contains(out, "forbidden") {
		t.Fatalf("a cashier reached the settings page: %s", out)
	}
}

func TestIDsAreUUIDs(t *testing.T) {
	a, b := app.NewID(), app.NewID()
	if a == b || len(a) != 36 || a[14] != '4' {
		t.Fatalf("ids %q %q", a, b)
	}
}

// The names the code uses are the names the manifest declares.
func TestNamesMatchTheManifest(t *testing.T) {
	b, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID        string `json:"id"`
		Schedules []struct {
			Event string `json:"event"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != app.PluginID || len(m.Schedules) != 1 || m.Schedules[0].Event != app.TickEvent {
		t.Fatalf("manifest %+v vs %s / %s", m, app.PluginID, app.TickEvent)
	}
}
