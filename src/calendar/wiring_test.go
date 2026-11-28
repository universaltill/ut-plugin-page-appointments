package calendar

import (
	"fmt"
	"testing"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// N4: bookings of an inactive staff member stay on the day view, openable.
func TestDayView_InactiveStaffWithBookingsStaysVisible(t *testing.T) {
	f := newFake(t)
	f.staff[0].Active = false
	f.add(t, "a1", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	ans := run(t, newDeps(f), ask("", now8(), manager, nil))
	has(t, ans.Document, "Ana (inactive)")
	if !hasAction(ans.Document, "open_a1") {
		t.Fatal("no open button for the inactive staff member's booking")
	}
	// without a booking the inactive member is not drawn
	f2 := newFake(t)
	f2.staff[0].Active = false
	lacks(t, run(t, newDeps(f2), ask("", now8(), manager, nil)).Document, "(inactive)")
}

// N15: a long queue cannot push the page past the component limit.
func TestQueue_ButtonsCapped(t *testing.T) {
	f := newFake(t)
	for i := 0; i < maxQueueButtons+15; i++ {
		f.queue = append(f.queue, Queued{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{
			ID: fmt.Sprintf("w%d", i), Seq: int64(i + 1), Arrived: jan14(t, 9, 0), DurationMin: 30}})
	}
	ans := run(t, newDeps(f), ask("", jan14(t, 9, 30), cashier, nil)) // run() validates the document
	has(t, ans.Document, "Only the first 20 waiting walk-ins have buttons.")
	seats := 0
	for _, c := range ans.Document.Components {
		if a, _ := c["action"].(string); len(a) > 5 && a[:5] == "seat_" {
			seats++
		}
	}
	if seats != maxQueueButtons {
		t.Fatalf("seat buttons = %d, want %d", seats, maxQueueButtons)
	}
}
