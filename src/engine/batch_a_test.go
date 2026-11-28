package engine

import (
	"errors"
	"reflect"
	"testing"
)

// reschedule builds the superseding row for A (10:00 -> 14:00) next to the
// original, as a store without a "current rows" filter would return them.
func rescheduledPair(t *testing.T) []Appointment {
	t.Helper()
	old := appt("a", "r1", Booked, jan14(t, 10, 0), 30, 0)
	nw := appt("a2", "r1", Booked, jan14(t, 14, 0), 30, 0)
	nw.SupersedesID = "a"
	return []Appointment{old, nw}
}

func TestCurrent_DropsSupersededRows(t *testing.T) {
	rows := rescheduledPair(t)
	got := Current(rows)
	if len(got) != 1 || got[0].ID != "a2" {
		t.Fatalf("got %+v", got)
	}
	// chain a -> a2 -> a3 keeps only a3; input is not modified
	a3 := appt("a3", "r1", Booked, jan14(t, 15, 0), 30, 0)
	a3.SupersedesID = "a2"
	all := append(append([]Appointment(nil), rows...), a3)
	if g := Current(all); len(g) != 1 || g[0].ID != "a3" || len(all) != 3 {
		t.Fatalf("chain %+v", g)
	}
	if g := Current(nil); len(g) != 0 {
		t.Fatalf("nil %+v", g)
	}
}

func TestF11_SupersededRowDoesNotHoldTime_Conflict(t *testing.T) {
	// reschedule A from 10:00 to 14:00, then book B at 10:00: no conflict
	b := cand(t, 10, 0, 10, 30, 0)
	b.ID = "b"
	if _, err := CheckConflict(b, rescheduledPair(t), DefaultPolicy(), nil, fakeClock{jan14(t, 8, 0)}, counterIDs("ev")); err != nil {
		t.Fatalf("old slot must be free, got %v", err)
	}
	// the new slot is held
	c := cand(t, 14, 0, 14, 30, 0)
	c.ID = "c"
	r, err := CheckConflict(c, rescheduledPair(t), DefaultPolicy(), nil, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(r.ConflictingIDs, []string{"a2"}) {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestF11_SupersededRowDoesNotHoldTime_Availability(t *testing.T) {
	r := res("r1", 540, 720)
	base := avail(t, r, req(30, 0), nil, jan14(t, 8, 0))
	got := avail(t, r, req(30, 0), rescheduledPair(t), jan14(t, 8, 0))
	if e := excluded(t, base, got); len(e) != 0 {
		t.Fatalf("the old slot must be free again, still excluded %v", e)
	}
}

func TestF11_SupersededRowDoesNotHoldTime_Walkin(t *testing.T) {
	r := res("r1", 540, 1080)
	e := est(t, []Resource{r}, rescheduledPair(t), nil, Walkin{DurationMin: 30}, jan14(t, 10, 0))
	if !e.OK || !e.Start.Equal(jan14(t, 10, 0)) {
		t.Fatalf("%+v", e)
	}
}

func TestF13_AvailabilityExcludesOwnAppointment(t *testing.T) {
	r := res("r1", 540, 720)
	own := appt("a", "r1", Booked, jan14(t, 10, 0), 30, 0)
	loc := london(t)
	plain, err := Availability(loc, jan14Date, r, req(30, 0), []Appointment{own}, DefaultPolicy(), jan14(t, 8, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range plain {
		if s.Start.Equal(jan14(t, 10, 0)) {
			t.Fatal("without exclusion the own slot is hidden")
		}
	}
	ex, err := AvailabilityExcluding(loc, jan14Date, r, req(30, 0), []Appointment{own}, "a", DefaultPolicy(), jan14(t, 8, 0))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range ex {
		found = found || s.Start.Equal(jan14(t, 10, 0))
	}
	if !found || len(ex) != len(plain)+3 {
		t.Fatalf("own slot and its 15 minute shifts must be offered: %v", hm(ex, loc))
	}
	// an unrelated id excludes nothing
	other, _ := AvailabilityExcluding(loc, jan14Date, r, req(30, 0), []Appointment{own}, "zzz", DefaultPolicy(), jan14(t, 8, 0))
	if !reflect.DeepEqual(other, plain) {
		t.Fatal("unrelated exclude id changed the result")
	}
}

func TestF13_AvailabilityAnyExcluding(t *testing.T) {
	rs := []Resource{res("r1", 540, 720), res("r2", 540, 720)}
	own := appt("a", "r1", Booked, jan14(t, 10, 0), 30, 0)
	loc := london(t)
	with, err := AvailabilityAnyExcluding(loc, jan14Date, rs, req(30, 0), []Appointment{own}, "a", DefaultPolicy(), jan14(t, 8, 0))
	if err != nil {
		t.Fatal(err)
	}
	without, _ := AvailabilityAny(loc, jan14Date, rs, req(30, 0), []Appointment{own}, DefaultPolicy(), jan14(t, 8, 0))
	if len(with) <= len(without) {
		t.Fatalf("%d vs %d", len(with), len(without))
	}
	if _, err := AvailabilityAnyExcluding(loc, jan14Date, rs, Request{}, nil, "a", DefaultPolicy(), jan14(t, 8, 0)); !errors.Is(err, ErrNoServices) {
		t.Fatal(err)
	}
}

func TestF14_OverrideNeedsActor(t *testing.T) {
	ex := []Appointment{appt("a", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	for _, actor := range []string{"", "   "} {
		r, err := CheckConflict(cand(t, 10, 0, 10, 30, 0), ex, overridePolicy(),
			&Override{ByManager: true, Reason: "regular", ActorUserID: actor}, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
		if !errors.Is(err, ErrInvalid) || len(r.Events) != 0 {
			t.Fatalf("actor %q: %v %+v", actor, err, r)
		}
	}
}
