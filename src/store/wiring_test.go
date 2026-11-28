package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

func entry(id string, seq int64, arrived time.Time, name string) store.QueueEntry {
	return store.QueueEntry{ID: id, Seq: seq, Arrived: arrived, FirstName: name, ServiceSKU: "cut", DurationMin: 30, BufferMin: 5, ResourceIDs: []string{"r1"}}
}

// N9 / B4: the queue is persisted, ordered, and a seated walk-in (same id as
// an appointment) is never listed even if the dequeue never ran.
func TestQueue_RoundTripSeatedHiddenAndPurgedAtDayEnd(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		day := at(14, 0, 0)
		if err := e.repo.Enqueue(ctx, entry("w1", 1, at(14, 9, 0), "Ann")); err != nil {
			t.Fatal(err)
		}
		if err := e.repo.Enqueue(ctx, entry("w2", 2, at(14, 9, 5), "")); err != nil {
			t.Fatal(err)
		}
		if err := e.repo.Enqueue(ctx, entry("w3", 3, at(14, 9, 6), "Bob1")); !errors.Is(err, store.ErrNameChars) {
			t.Fatalf("digit name accepted: %v", err)
		}
		q, err := e.repo.Queue(ctx, day)
		if err != nil || len(q) != 2 || q[0].ID != "w1" || q[0].FirstName != "Ann" || q[0].ResourceIDs[0] != "r1" || q[1].Seq != 2 {
			t.Fatalf("queue = %+v %v", q, err)
		}
		// seat w1: an appointment with the queue id, no Dequeue (crash)
		a := draft("r1", at(14, 9, 10), 30, 5)
		a.ID, a.Source, a.OriginTillID, a.State, a.WalkIn, a.CreatedAt = "w1", "own", "till1", engine.CheckedIn, true, at(14, 9, 10)
		if err := e.repo.Insert(ctx, store.Booking{Appointment: a, FirstName: "Ann"}, nil); err != nil {
			t.Fatal(err)
		}
		if q, _ := e.repo.Queue(ctx, day); len(q) != 1 || q[0].ID != "w2" {
			t.Fatalf("seated entry still listed: %+v", q)
		}
		// next day: the entry from yesterday is hidden and the purge deletes it
		e.clk.t = at(15, 8, 0)
		if q, _ := e.repo.Queue(ctx, at(15, 0, 0)); len(q) != 0 {
			t.Fatalf("yesterday's queue shown: %+v", q)
		}
		res, err := e.repo.Purge(ctx)
		if err != nil || res.Queue != 2 {
			t.Fatalf("purge: %+v %v", res, err)
		}
		if strings.Contains(e.dump(), "Ann") && strings.Contains(e.dump(), "walkin_queue") {
			// the name may live on the (not yet retained) appointment row, never in the queue
			if q, _ := e.repo.Queue(ctx, at(1, 0, 0)); len(q) != 0 {
				t.Fatalf("queue rows remain: %+v", q)
			}
		}
	})
}

// N13: a second row superseding the same row is refused (stale repost).
func TestInsert_OneSuperseder(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		old := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		mk := func(id string, h int) store.Booking {
			a := draft("r1", at(14, h, 0), 30, 0)
			a.ID, a.Source, a.OriginTillID, a.State, a.SupersedesID, a.CreatedAt = id, "own", "till1", engine.Booked, old.ID, at(14, 8, 1)
			return store.Booking{Appointment: a, FirstName: "Ann"}
		}
		if err := e.repo.Insert(ctx, mk("n1", 12), nil); err != nil {
			t.Fatal(err)
		}
		if err := e.repo.Insert(ctx, mk("n2", 13), nil); !errors.Is(err, store.ErrNotCurrent) {
			t.Fatalf("second superseder: %v", err)
		}
		if cur, _ := e.repo.Current(ctx, at(14, 0, 0), at(15, 0, 0), ""); len(cur) != 1 || cur[0].ID != "n1" {
			t.Fatalf("current = %+v", cur)
		}
	})
}

// SetState: optimistic check, and a superseded row takes no transition.
func TestSetState_OptimisticAndSuperseded(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		b := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		to := b.Appointment
		to.State = engine.Confirmed
		ev := engine.Event{ID: "e-x", AppointmentID: b.ID, At: at(14, 8, 0), Kind: engine.KindStateChange, FromState: engine.Booked, ToState: engine.Confirmed}
		if err := e.repo.SetState(ctx, b.ID, engine.Booked, to, []engine.Event{ev}); err != nil {
			t.Fatal(err)
		}
		if err := e.repo.SetState(ctx, b.ID, engine.Booked, to, nil); !errors.Is(err, store.ErrStale) {
			t.Fatalf("stale: %v", err)
		}
		if g, _ := e.repo.Get(ctx, b.ID); g.State != engine.Confirmed {
			t.Fatalf("state %s", g.State)
		}
	})
}

// N15: "Local" is not a shop zone.
func TestSettings_LocalZoneRefused(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		s := store.DefaultSettings()
		s.ShopZone = "Local"
		if err := e.repo.SaveSettings(ctx, s); err == nil {
			t.Fatal("Local accepted")
		}
	})
}

// N15: removing a time-off entry that no longer exists is not "saved".
func TestDeleteTimeOff_UnknownIsNotFound(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		if err := e.repo.DeleteTimeOff(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}
