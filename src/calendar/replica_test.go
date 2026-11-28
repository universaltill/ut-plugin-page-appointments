package calendar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/snapshot"
)

func replicaPayload(t *testing.T, asOf string) snapshot.Payload {
	t.Helper()
	raw := `{"v":1,"as_of":"` + asOf + `","zone":"Europe/London","from_utc":"2026-11-28T00:00:00Z","to_utc":"2026-12-01T00:00:00Z","truncated":true,
	"resources":[{"id":"r1","kind":"staff","name":"Sam","colour":"#2a7"}],
	"appointments":[{"id":"a1","r":"r1","s":"2026-11-28T10:30:00Z","e":"2026-11-28T11:00:00Z","b":0,"st":"booked","w":false,"n":"Ann","svc":["Skin fade"]},
	{"id":"a2","r":"r1","s":"2026-11-29T10:30:00Z","e":"2026-11-29T11:00:00Z","b":0,"st":"booked","w":true,"n":"","svc":[]}]}`
	p, err := snapshot.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func replicaText(t *testing.T, d *Document) string {
	t.Helper()
	if errs := CheckDocument(*d); len(errs) != 0 {
		t.Fatalf("invalid document: %v", errs)
	}
	b, _ := json.Marshal(d)
	return string(b)
}

func TestReplica_ReadOnlyDayFromSnapshot_7207(t *testing.T) {
	now := time.Date(2026, 11, 28, 9, 20, 0, 0, time.UTC)
	d := ReplicaDocument(replicaPayload(t, "2026-11-28T09:15:00Z"), true, ReplicaAsk{Now: now})
	s := replicaText(t, d)
	for _, want := range []string{"Ann", "Skin fade", "10:30-11:00", "Last synced 09:15", "appt.replica.readonly", "appt.replica.truncated", `"action":"day_20261129"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "appt.replica.stale") {
		t.Error("fresh snapshot called stale")
	}
	// no write path: the only buttons are day navigation
	for _, c := range d.Components {
		if c["type"] == "button" && !strings.HasPrefix(c["action"].(string), "day_") {
			t.Errorf("replica offers action %v", c["action"])
		}
		if c["type"] == "form" {
			t.Error("replica offers a form")
		}
	}
	// navigation stays inside the window; next day shows the other row, never today's
	next := ReplicaDocument(replicaPayload(t, "2026-11-28T09:15:00Z"), true, ReplicaAsk{Now: now, Action: "day_20261129"})
	ns := replicaText(t, next)
	if strings.Contains(ns, "Ann") || !strings.Contains(ns, "Walk-in") {
		t.Errorf("day 2 shows %s", ns)
	}
	far := ReplicaDocument(replicaPayload(t, "2026-11-28T09:15:00Z"), true, ReplicaAsk{Now: now, Action: "day_20270101"})
	if fs := replicaText(t, far); !strings.Contains(fs, "2026-11-30") || strings.Contains(fs, `"day_20261201"`) {
		t.Errorf("outside the window: %s", fs)
	}
}

func TestReplica_StaleExpiredAndMissing_7207(t *testing.T) {
	p := replicaPayload(t, "2026-11-28T09:15:00Z")
	stale := replicaText(t, ReplicaDocument(p, true, ReplicaAsk{Now: p0(t, "2026-11-28T11:16:00Z")}))
	if !strings.Contains(stale, "appt.replica.stale") || !strings.Contains(stale, "Ann") {
		t.Errorf("stale: %s", stale)
	}
	exp := replicaText(t, ReplicaDocument(p, true, ReplicaAsk{Now: p0(t, "2026-11-28T21:16:00Z")}))
	if !strings.Contains(exp, "Last synced 2026-11-28 09:15. Check the main till.") || strings.Contains(exp, "Ann") || strings.Contains(exp, "Skin fade") {
		t.Errorf("expired snapshot still lists appointments: %s", exp)
	}
	none := replicaText(t, ReplicaDocument(snapshot.Payload{}, false, ReplicaAsk{Now: p0(t, "2026-11-28T09:00:00Z")}))
	if !strings.Contains(none, "appt.replica.none") {
		t.Errorf("none: %s", none)
	}
}

func p0(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
