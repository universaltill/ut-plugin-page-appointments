package store_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// B1: purge, erase and reconcile must never read the whole table in one query
// (the host caps a result at 1 MiB). The cap here is tiny so 60 rows exceed it.
func TestB1_PurgeEraseReconcile_PageUnderResultCap(t *testing.T) {
	forEachBackendWith(t, capped(4000), func(t *testing.T, e *env) {
		e.repo.SetPageSize(10)
		var ids []string
		for i := 0; i < 60; i++ {
			b := mustCreate(t, e, "r1", at(14+i, 10, 0), fmt.Sprintf("Name%c", 'A'+i%26))
			ids = append(ids, b.ID)
		}
		if n, err := e.repo.Reconcile(ctx); err != nil || n != 0 {
			t.Fatalf("reconcile: %d %v", n, err)
		}
		// erase loads one chain, not the table
		if n, err := e.repo.Erase(ctx, ids[59], "owner1"); err != nil || n != 1 {
			t.Fatalf("erase: %d %v", n, err)
		}
		e.clk.t = at(14, 8, 0).Add(300 * 24 * time.Hour)
		r, err := e.repo.Purge(ctx)
		if err != nil || r.Rows != 59 || r.Chains != 59 {
			t.Fatalf("purge: %v %+v", err, r)
		}
		for _, id := range ids {
			if g, _ := e.repo.Get(ctx, id); g.FirstName != "" {
				t.Fatalf("%s still named", id)
			}
		}
		if r, err := e.repo.Purge(ctx); err != nil || r.Rows != 0 {
			t.Fatalf("second purge: %v %+v", err, r)
		}
	})
}

// The cap double itself works (so the test above can fail for the right reason).
func TestB1_CapDoubleRefusesBigResults(t *testing.T) {
	forEachBackendWith(t, capped(300), func(t *testing.T, e *env) {
		for i := 0; i < 5; i++ {
			mustCreate(t, e, "r1", at(14, 8+i, 0), "Ann")
		}
		if _, err := e.repo.Current(ctx, at(14, 0, 0), at(15, 0, 0), ""); err == nil {
			t.Fatal("expected the cap to refuse a big result")
		}
	})
	if hostMaxResultBytes != 1048576 {
		t.Fatal("host cap")
	}
}

// B2: create with an existing id never adds lines to an immutable booking.
func TestB2_CreateExistingID(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		nb := store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Ann", ActorUserID: "u1"}
		nb.Appointment.ID = "fixed"
		first, err := e.repo.Create(ctx, nb)
		if err != nil {
			t.Fatal(err)
		}
		evs, _ := e.repo.Events(ctx, "fixed")
		// identical retry: the existing booking comes back, nothing is added
		again, err := e.repo.Create(ctx, nb)
		if err != nil || again.ID != first.ID || len(again.Lines) != 1 {
			t.Fatalf("retry: %v %+v", err, again)
		}
		// retry with a changed service list: refused, nothing added
		ch := nb
		ch.Appointment.Lines = []engine.Line{line("cut", 30, 0), line("beard", 15, 0)}
		ch.Appointment.End = at(14, 10, 45)
		if _, err := e.repo.Create(ctx, ch); !errors.Is(err, store.ErrIDExists) {
			t.Fatalf("changed retry: %v", err)
		}
		g, _ := e.repo.Get(ctx, "fixed")
		if len(g.Lines) != 1 {
			t.Fatalf("lines grew: %+v", g.Lines)
		}
		if evs2, _ := e.repo.Events(ctx, "fixed"); len(evs2) != len(evs) {
			t.Fatal("events grew")
		}
	})
}

// Orphan lines (a crash before the appointment row) are compared, not mixed.
func TestB2_OrphanLinesWithChangedListRefused(t *testing.T) {
	d := newMem()
	repo := store.New(d, &clock{t: at(14, 8, 0)}, counter("id"), "till1")
	d.FailExec = func(q string, _ []any) error {
		if q == store.SQLInsertAppointment || q == store.SQLInsertAppointmentFree {
			return errors.New("crash")
		}
		return nil
	}
	nb := store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Ann"}
	nb.Appointment.ID = "fixed"
	if _, err := repo.Create(ctx, nb); err == nil {
		t.Fatal("expected failure")
	}
	d.FailExec = nil
	ch := nb
	ch.Appointment.Lines = []engine.Line{line("cut", 30, 0), line("beard", 15, 0)}
	ch.Appointment.End = at(14, 10, 45)
	if _, err := repo.Create(ctx, ch); !errors.Is(err, store.ErrIDExists) {
		t.Fatalf("%v", err)
	}
	if _, err := repo.Get(ctx, "fixed"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a refused create wrote the row")
	}
}

// B3: order by insert order, never by the wall clock.
func TestB3_ClockSteppedBack_NoFalseReconcile(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		b := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		e.clk.t = e.clk.t.Add(-2 * time.Hour) // NTP / operator corrected the time
		if _, err := e.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.Confirmed, ActorUserID: "u1"}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			if n, err := e.repo.Reconcile(ctx); err != nil || n != 0 {
				t.Fatalf("call %d: reconcile repaired %d (%v)", i, n, err)
			}
		}
		evs, _ := e.repo.Events(ctx, b.ID)
		if len(evs) != 2 || evs[0].ToState != engine.Booked || evs[1].ToState != engine.Confirmed {
			t.Fatalf("history out of order: %+v", evs)
		}
	})
}

// N1: a manager can always undo a no-show; an overlap writes a
// conflict_override event whatever the double-booking setting.
func TestN1_UndoNoShowOverlap_AlwaysAllowedWithOverrideEvent(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) { // default policy: no double booking
		a := mustCreate(t, e, "r1", at(14, 10, 0), "Ana")
		e.clk.t = at(14, 10, 16)
		if _, err := e.repo.Transition(ctx, a.ID, engine.TransitionInput{To: engine.NoShow, ActorUserID: "u1"}); err != nil {
			t.Fatal(err)
		}
		w := draft("r1", at(14, 10, 20), 30, 0)
		w.WalkIn = true
		wb, err := e.repo.Create(ctx, store.NewBooking{Appointment: w, FirstName: "Wes", ActorUserID: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		e.clk.t = at(14, 10, 25)
		out, err := e.repo.Transition(ctx, a.ID, engine.TransitionInput{To: engine.CheckedIn, ActorUserID: "m1", ByManager: true, Reason: "arrived 10:25"})
		if err != nil || out.State != engine.CheckedIn {
			t.Fatalf("%v %+v", err, out)
		}
		evs, _ := e.repo.Events(ctx, a.ID)
		last := evs[len(evs)-1]
		if last.Kind != engine.KindConflictOverride || last.Reason != "arrived 10:25" || len(last.ConflictingIDs) != 1 || last.ConflictingIDs[0] != wb.ID {
			t.Fatalf("%+v", evs)
		}
	})
}

// N11: the purge event is written before the names are blanked.
func TestN11_PurgeEventBeforeBlanking(t *testing.T) {
	d := newMem()
	clk := &clock{t: at(14, 8, 0)}
	repo := store.New(d, clk, counter("id"), "till1")
	b, err := repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Ann", ActorUserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	clk.t = at(14, 8, 0).Add(100 * 24 * time.Hour)
	d.FailExec = func(q string, _ []any) error { // crash when the blanking starts
		if q == store.SQLBlankName {
			return errors.New("crash")
		}
		return nil
	}
	if _, err := repo.Purge(ctx); err == nil {
		t.Fatal("expected failure")
	}
	d.FailExec = nil
	evs, _ := repo.Events(ctx, b.ID)
	if evs[len(evs)-1].Kind != store.KindPurge {
		t.Fatalf("purge event must exist before the blanking: %+v", evs)
	}
	r, err := repo.Purge(ctx) // the rerun blanks, without a second event
	if err != nil || r.Rows != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	evs2, _ := repo.Events(ctx, b.ID)
	if len(evs2) != len(evs) {
		t.Fatalf("a second purge event was written: %+v", evs2)
	}
	if g, _ := repo.Get(ctx, b.ID); g.FirstName != "" {
		t.Fatal("still named")
	}
	if !strings.Contains(fmt.Sprint(evs2[len(evs2)-1].Reason), store.ReasonRetention) {
		t.Fatal("reason")
	}
}
