//go:build !wasip1

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/app"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/snapshot"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

func snapRig(t *testing.T, replica bool) (*rig, *plugin.FakeHost) {
	t.Helper()
	h := plugin.NewFakeHost()
	h.SnapshotCollections = []string{"calendar"}
	h.Replica = replica
	plugin.UseFakeHost(t, h)
	r := &rig{db: newRig(t).db, now: time.Date(2026, 1, 14, 9, 0, 0, 0, time.UTC)}
	r.h = app.Handlers(app.Deps{DB: r.db, Catalogue: cat{}, Receipts: rec{}, Pub: &pub{}, Records: app.HostRecords{},
		IDs: func() string { r.n++; return fmt.Sprintf("id-%d", r.n) }})
	return r, h
}

func seed(t *testing.T, r *rig) {
	repo := store.New(r.db, nil, nil, "main")
	ctx := context.Background()
	_ = repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "st1", Kind: "staff", Active: true}, DisplayName: "Ana"})
	_ = repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, All: true, Active: true})
}

// #7207: a walk-in on the main till publishes the snapshot; the tick refreshes
// it only when the content changed or the hour passed.
func TestMainPublishesSnapshotOnChangeAndTick(t *testing.T) {
	r, h := snapRig(t, false)
	seed(t, r)
	out := r.send(t, "ui.action.ask", map[string]any{"view": "appointments.day", "action": "queue_add", "locale": "en",
		"form": map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"}})
	if !strings.Contains(out, "Sam") {
		t.Fatalf("queue_add: %s", out)
	}
	raw, ok := h.Snapshots["calendar"]
	if !ok {
		t.Fatal("no snapshot written after an action")
	}
	p, err := snapshot.Decode(raw)
	if err != nil || len(p.Resources) != 1 || p.Resources[0].Name != "Ana" || p.Zone != "Europe/London" {
		t.Fatalf("payload %s %v", raw, err)
	}
	if strings.Contains(string(raw), "phone") || strings.Contains(string(raw), "email") {
		t.Fatal("contact data in the snapshot")
	}
	// a tick 15 minutes later with nothing changed does not rewrite (as_of kept)
	first := string(raw)
	r.now = r.now.Add(15 * time.Minute)
	r.send(t, app.TickEvent, map[string]any{})
	if string(h.Snapshots["calendar"]) != first {
		t.Fatal("an unchanged tick rewrote the snapshot")
	}
	// an hour on, the tick refreshes as_of
	r.now = r.now.Add(46 * time.Minute)
	r.send(t, app.TickEvent, map[string]any{})
	if string(h.Snapshots["calendar"]) == first {
		t.Fatal("the hourly refresh did not run")
	}
}

// A view never writes the snapshot; a refused snapshot_set never fails an action.
func TestSnapshotErrorsNeverBlock(t *testing.T) {
	r, h := snapRig(t, false)
	seed(t, r)
	r.send(t, "ui.view.ask", map[string]any{"view": "appointments.day", "locale": "en"})
	if len(h.Snapshots) != 0 {
		t.Fatal("a view wrote the snapshot")
	}
	h.SnapshotCollections = nil // manifest without records_forward: every call denied
	out := r.send(t, "ui.action.ask", map[string]any{"view": "appointments.day", "action": "queue_add", "locale": "en",
		"form": map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"}})
	if !strings.Contains(out, "Sam added to the queue") {
		t.Fatalf("action failed with the channel denied: %s", out)
	}
}

// On a replica every page is the read-only snapshot and nothing is written.
func TestReplicaShowsSnapshotAndWritesNothing(t *testing.T) {
	r, h := snapRig(t, true)
	asOf := r.now.Add(-5 * time.Minute).Format(time.RFC3339)
	h.Snapshots["calendar"] = []byte(`{"v":1,"as_of":"` + asOf + `","zone":"Europe/London","from_utc":"2026-01-14T00:00:00Z","to_utc":"2026-01-17T00:00:00Z","truncated":false,
	"resources":[{"id":"r1","kind":"staff","name":"Sam","colour":""}],
	"appointments":[{"id":"a1","r":"r1","s":"2026-01-14T10:30:00Z","e":"2026-01-14T11:00:00Z","b":0,"st":"booked","w":false,"n":"Ann","svc":["Cut"]}]}`)
	for _, v := range []string{"appointments.day", "appointments.today", "appointments.settings"} {
		out := r.send(t, "ui.view.ask", map[string]any{"view": v, "locale": "en"})
		if !strings.Contains(out, "Ann") || !strings.Contains(out, "appt.replica.readonly") {
			t.Fatalf("%s on a replica: %s", v, out)
		}
	}
	// an action (a booking attempt) is ignored: no write, same read-only page
	before := len(r.db.Dump())
	out := r.send(t, "ui.action.ask", map[string]any{"view": "appointments.day", "action": "queue_add", "locale": "en",
		"form": map[string]any{"first_name": "Zed", "service": "cut", "staff": "any"}})
	if strings.Contains(out, "Zed") || len(r.db.Dump()) != before {
		t.Fatalf("a replica wrote: %s", out)
	}
	if len(h.Snapshots) != 1 || !strings.Contains(string(h.Snapshots["calendar"]), "Ann") {
		t.Fatal("replica changed the snapshot")
	}
	// tick on a replica writes no snapshot either
	r.send(t, app.TickEvent, map[string]any{})
	var v map[string]any
	_ = json.Unmarshal(h.Snapshots["calendar"], &v)
	if v["as_of"] != asOf {
		t.Fatal("replica tick rewrote the snapshot")
	}
}

// No snapshot yet (never paired): the empty state, not an error.
func TestReplicaWithoutSnapshot(t *testing.T) {
	r, _ := snapRig(t, true)
	out := r.send(t, "ui.view.ask", map[string]any{"view": "appointments.day", "locale": "en"})
	if !strings.Contains(out, "appt.replica.none") {
		t.Fatalf("%s", out)
	}
}
