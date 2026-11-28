package adapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/checkout"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// The calendar decides a transition on the state it read; another event moves
// the row before the commit. The write must compare against the state the
// decision was made on, not a fresh read (review fix-plug finding 1).
func TestUpdateRefusedWhenStateMovedAfterDecision(t *testing.T) {
	r := newRig(t)
	b := r.bookAt(10, "Ana")
	ctx := context.Background()
	// decision: booked -> cancelled, made on the booked row
	next := b.Appointment
	next.State = engine.CancelledByShop
	// meanwhile another event checks the client in
	if _, err := r.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.CheckedIn, ActorUserID: "u", Now: time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	err := r.st.Commit(calendar.Change{From: engine.Booked, Update: &calendar.Booking{Appointment: next, FirstName: "Ana"}})
	if !errors.Is(err, calendar.ErrGone) {
		t.Fatalf("stale decision committed: %v", err)
	}
	if g, _ := r.repo.Get(ctx, b.ID); g.State != engine.CheckedIn {
		t.Fatalf("state overwritten: %s", g.State)
	}
}

// An update without the decided-on state is refused, never applied against
// a fresh read.
func TestUpdateWithoutFromStateRefused(t *testing.T) {
	r := newRig(t)
	b := r.bookAt(10, "Ana")
	next := b.Appointment
	next.State = engine.CancelledByShop
	if err := r.st.Commit(calendar.Change{Update: &calendar.Booking{Appointment: next}}); err == nil {
		t.Fatal("update without From applied")
	}
}

// A second completion with another sale must not overwrite sale_ref while the
// outbox keeps the first one.
func TestSecondCompletionDoesNotOverwriteSaleRef(t *testing.T) {
	r, _, _, co := deskRig(t)
	b := r.bookAt(10, "Ana")
	ctx := context.Background()
	if _, err := r.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.CheckedIn, ActorUserID: "u", Now: time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	done := func(sale string) engine.Appointment {
		a := b.Appointment
		a.State, a.SaleRef = engine.Completed, sale
		return a
	}
	if err := co.Commit(engine.CheckedIn, done("sale-1"), nil, checkout.Completion{AppointmentID: b.ID, SaleRef: "sale-1", Payload: []byte(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	// the second completion was decided on the checked_in row too
	err := co.Commit(engine.CheckedIn, done("sale-2"), nil, checkout.Completion{AppointmentID: b.ID, SaleRef: "sale-2", Payload: []byte(`{"a":2}`)})
	if !errors.Is(err, checkout.ErrConflict) {
		t.Fatalf("second completion: %v", err)
	}
	g, _ := r.repo.Get(ctx, b.ID)
	if g.SaleRef != "sale-1" {
		t.Fatalf("sale_ref overwritten: %q", g.SaleRef)
	}
	pend, _ := co.Pending()
	if len(pend) != 1 || pend[0].SaleRef != "sale-1" {
		t.Fatalf("outbox = %+v", pend)
	}
}
