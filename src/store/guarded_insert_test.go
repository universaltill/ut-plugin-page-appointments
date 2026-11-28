package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

func decided(id, res string, start time.Time, supersedes string) store.Booking {
	a := draft(res, start, 30, 5)
	a.ID, a.Source, a.OriginTillID, a.State, a.SupersedesID, a.CreatedAt = id, "own", "till1", engine.Booked, supersedes, at(14, 8, 1)
	return store.Booking{Appointment: a, FirstName: "Ann"}
}

// Two events checked the same free slot concurrently (the host runs up to 4
// events of one plugin at once) and both reach Insert. The insert is one
// guarded statement (INSERT ... SELECT ... WHERE NOT EXISTS overlap), so the
// second is refused with engine.ErrConflict and leaves no row (its lines stay
// unreferenced, as after a crash; lines are never deleted)
// (fix-plug finding 2).
func TestInsert_GuardedAgainstConcurrentDoubleBooking(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		if err := e.repo.Insert(ctx, decided("a1", "r1", at(14, 10, 0), ""), nil); err != nil {
			t.Fatal(err)
		}
		// overlaps a1's hold (10:00-10:35) on the same resource
		err := e.repo.Insert(ctx, decided("a2", "r1", at(14, 10, 30), ""), nil)
		if !errors.Is(err, engine.ErrConflict) {
			t.Fatalf("double booking written: %v", err)
		}
		if _, err := e.repo.Get(ctx, "a2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a2 row: %v", err)
		}
		// adjacent (after the buffer), another resource, and a move of a1
		// itself over its own slot are free
		for _, b := range []store.Booking{decided("a3", "r1", at(14, 10, 35), ""), decided("a4", "r2", at(14, 10, 0), ""),
			decided("a5", "r1", at(14, 9, 55), "a1")} {
			if err := e.repo.Insert(ctx, b, nil); err != nil {
				t.Fatalf("%s: %v", b.ID, err)
			}
		}
		// a row that does not hold time (cancelled) does not block
		if err := e.repo.SetState(ctx, "a3", engine.Booked, func() engine.Appointment {
			x := decided("a3", "r1", at(14, 10, 35), "").Appointment
			x.State = engine.CancelledByShop
			return x
		}(), nil); err != nil {
			t.Fatal(err)
		}
		if err := e.repo.Insert(ctx, decided("a6", "r1", at(14, 10, 40), ""), nil); err != nil {
			t.Fatalf("cancelled row blocked: %v", err)
		}
		// a manager override (a conflict_override event) is written anyway
		ov := engine.Event{ID: "ov1", AppointmentID: "a7", At: at(14, 8, 1), Kind: engine.KindConflictOverride, Reason: "squeeze", ActorUserID: "m"}
		if err := e.repo.Insert(ctx, decided("a7", "r1", at(14, 10, 40), ""), []engine.Event{ov}); err != nil {
			t.Fatalf("override refused: %v", err)
		}
	})
}
