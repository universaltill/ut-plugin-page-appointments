package engine

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func cand(t *testing.T, sh, sm, eh, em, buf int) Appointment {
	return Appointment{ID: "new", ResourceID: "r1", State: Booked, Start: jan14(t, sh, sm), End: jan14(t, eh, em), BufferMin: buf}
}

func check(t *testing.T, c Appointment, ex []Appointment) error {
	t.Helper()
	_, err := CheckConflict(c, ex, DefaultPolicy(), nil, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
	return err
}

func TestConflict_OneMinute(t *testing.T) {
	ex := []Appointment{appt("e1", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	r, err := CheckConflict(cand(t, 10, 29, 10, 59, 0), ex, DefaultPolicy(), nil, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(r.ConflictingIDs, []string{"e1"}) {
		t.Fatalf("%v %v", err, r)
	}
}

func TestConflict_Touching(t *testing.T) {
	ex := []Appointment{appt("e1", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	if err := check(t, cand(t, 10, 30, 11, 0, 0), ex); err != nil {
		t.Fatal(err)
	}
	if err := check(t, cand(t, 9, 30, 10, 0, 0), ex); err != nil {
		t.Fatal(err)
	}
}

func TestConflict_Buffer(t *testing.T) {
	ex := []Appointment{appt("e1", "r1", Booked, jan14(t, 10, 0), 30, 10)}
	cases := []struct {
		c    Appointment
		want bool
	}{
		{cand(t, 10, 39, 11, 0, 0), true}, {cand(t, 10, 40, 11, 0, 0), false},
		{cand(t, 9, 50, 10, 5, 0), true}, {cand(t, 9, 30, 10, 0, 0), false}, {cand(t, 9, 30, 10, 0, 1), true},
	}
	for i, c := range cases {
		if got := errors.Is(check(t, c.c, ex), ErrConflict); got != c.want {
			t.Errorf("case %d: conflict=%v want %v", i, got, c.want)
		}
	}
}

func TestConflict_OtherResourceAndSelf(t *testing.T) {
	ex := []Appointment{appt("e1", "r2", Booked, jan14(t, 10, 0), 30, 0)}
	if err := check(t, cand(t, 10, 0, 10, 30, 0), ex); err != nil {
		t.Fatal(err)
	}
	ex = []Appointment{appt("new", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	if err := check(t, cand(t, 10, 0, 10, 30, 0), ex); err != nil {
		t.Fatal("same id must be ignored")
	}
	ex = []Appointment{appt("old", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	c := cand(t, 10, 0, 10, 30, 0)
	c.SupersedesID = "old"
	if err := check(t, c, ex); err != nil {
		t.Fatal("superseded row must be ignored")
	}
}

func TestConflict_NonHoldingStates(t *testing.T) {
	for st, blocks := range map[State]bool{Booked: true, Confirmed: true, CheckedIn: true, InService: true,
		Completed: false, CancelledByClient: false, CancelledByShop: false, NoShow: false} {
		ex := []Appointment{appt("e1", "r1", st, jan14(t, 10, 0), 30, 0)}
		if got := errors.Is(check(t, cand(t, 10, 0, 10, 30, 0), ex), ErrConflict); got != blocks {
			t.Errorf("%s: %v", st, got)
		}
	}
}

func TestConflict_Validation(t *testing.T) {
	good := cand(t, 10, 0, 10, 30, 0)
	mut := []func(*Appointment){
		func(a *Appointment) { a.ID = "" }, func(a *Appointment) { a.ResourceID = "" },
		func(a *Appointment) { a.End = a.Start }, func(a *Appointment) { a.BufferMin = -1 },
	}
	for i, m := range mut {
		c := good
		m(&c)
		if err := check(t, c, nil); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func overridePolicy() Policy { p := DefaultPolicy(); p.AllowDoubleBooking = true; return p }

func TestConflict_OverrideOK(t *testing.T) {
	ex := []Appointment{appt("b", "r1", Booked, jan14(t, 10, 0), 30, 0), appt("a", "r1", Confirmed, jan14(t, 10, 0), 30, 0)}
	now := jan14(t, 8, 0)
	r, err := CheckConflict(cand(t, 10, 0, 10, 30, 0), ex, overridePolicy(),
		&Override{ByManager: true, Reason: "regular client, two chairs free later", ActorUserID: "u1"}, fakeClock{now}, counterIDs("ev"))
	if err != nil || len(r.Events) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	e := r.Events[0]
	if e.Kind != "conflict_override" || e.Reason != "regular client, two chairs free later" || e.ActorUserID != "u1" ||
		e.At != now || !reflect.DeepEqual(e.ConflictingIDs, []string{"a", "b"}) || e.AppointmentID != "new" || e.ID != "ev1" {
		t.Fatalf("event %+v", e)
	}
}

func TestConflict_OverrideRefused(t *testing.T) {
	ex := []Appointment{appt("a", "r1", Booked, jan14(t, 10, 0), 30, 0)}
	c := cand(t, 10, 0, 10, 30, 0)
	cases := []struct {
		name string
		p    Policy
		ov   Override
		want error
	}{
		{"policy off", DefaultPolicy(), Override{ByManager: true, Reason: "x"}, ErrOverrideNotAllowed},
		{"not manager", overridePolicy(), Override{Reason: "x"}, ErrOverrideNotAllowed},
		{"empty reason", overridePolicy(), Override{ByManager: true}, ErrReasonRequired},
		{"blank reason", overridePolicy(), Override{ByManager: true, Reason: "   "}, ErrReasonRequired},
		{"501 runes", overridePolicy(), Override{ByManager: true, Reason: strings.Repeat("é", 501)}, ErrInvalid},
	}
	for _, tc := range cases {
		ov := tc.ov
		r, err := CheckConflict(c, ex, tc.p, &ov, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
		if !errors.Is(err, tc.want) || len(r.Events) != 0 {
			t.Errorf("%s: %v %+v", tc.name, err, r)
		}
	}
	ok := Override{ByManager: true, Reason: strings.Repeat("é", 500)}
	if _, err := CheckConflict(c, ex, overridePolicy(), &ok, fakeClock{jan14(t, 8, 0)}, counterIDs("ev")); err != nil {
		t.Fatal("500 runes should pass")
	}
}

func TestConflict_OverrideNoConflict(t *testing.T) {
	r, err := CheckConflict(cand(t, 10, 0, 10, 30, 0), nil, overridePolicy(),
		&Override{ByManager: true, Reason: "x"}, fakeClock{jan14(t, 8, 0)}, counterIDs("ev"))
	if err != nil || len(r.Events) != 0 {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestPolicy_Validate(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	d := DefaultPolicy()
	if d.SlotStepMin != 15 || d.NoShowGraceMin != 15 || d.LateCancelHours != 24 || d.AllowDoubleBooking || d.OverrunMin != 5 {
		t.Fatalf("defaults %+v", d)
	}
	for _, step := range []int{5, 10, 15, 20, 30, 60} {
		p := d
		p.SlotStepMin = step
		if p.Validate() != nil {
			t.Errorf("step %d", step)
		}
	}
	bad := []func(*Policy){
		func(p *Policy) { p.SlotStepMin = 7 }, func(p *Policy) { p.NoShowGraceMin = -1 },
		func(p *Policy) { p.LateCancelHours = -1 }, func(p *Policy) { p.OverrunMin = -1 },
	}
	for i, m := range bad {
		p := d
		m(&p)
		if !errors.Is(p.Validate(), ErrInvalidPolicy) {
			t.Errorf("bad %d", i)
		}
	}
	_ = time.Second
}
