package engine

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// allowedRows is typed in literally (not derived from the production table).
var allowedRows = map[[2]State]bool{
	{Booked, Confirmed}: true, {Booked, CheckedIn}: true, {Booked, CancelledByClient}: true,
	{Booked, CancelledByShop}: true, {Booked, NoShow}: true,
	{Confirmed, CheckedIn}: true, {Confirmed, CancelledByClient}: true, {Confirmed, CancelledByShop}: true,
	{Confirmed, NoShow}:    true,
	{CheckedIn, InService}: true, {CheckedIn, CancelledByClient}: true, {CheckedIn, CancelledByShop}: true,
	{InService, Completed}: true,
	// ADR-0221 B8 / legal 7: additive corrections.
	{CheckedIn, Booked}: true, {InService, CancelledByShop}: true, {NoShow, CheckedIn}: true,
}

var allStates = []State{Booked, Confirmed, CheckedIn, InService, Completed, CancelledByClient, CancelledByShop, NoShow}

func TestTransitions_AllPairs(t *testing.T) {
	if len(allowedRows) != 16 {
		t.Fatal("expected 16 allowed rows")
	}
	start := jan14(t, 14, 0)
	now := start.Add(time.Hour) // after grace, so no_show is allowed
	n := 0
	for _, from := range allStates {
		for _, to := range allStates {
			n++
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				a := appt("a1", "r1", from, start, 30, 0)
				got, ev, err := Transition(a, TransitionInput{To: to, ActorUserID: "u1", SaleRef: "s-1", Now: now, Reason: "because", ByManager: true}, DefaultPolicy(), counterIDs("ev"))
				if allowedRows[[2]State{from, to}] {
					if err != nil || got.State != to {
						t.Fatalf("want ok, got %v state %s", err, got.State)
					}
					if ev.Kind != "state_change" || ev.FromState != from || ev.ToState != to || ev.At != now ||
						ev.ActorUserID != "u1" || ev.AppointmentID != "a1" || ev.ID != "ev1" {
						t.Fatalf("event %+v", ev)
					}
					return
				}
				if !errors.Is(err, ErrTransitionNotAllowed) || got.State != from || ev.ID != "" {
					t.Fatalf("want refused, got %v %s %+v", err, got.State, ev)
				}
			})
		}
	}
	if n != 64 {
		t.Fatal(n)
	}
}

func TestTransitions_StateHelpers(t *testing.T) {
	for _, s := range allStates {
		wantHold := s == Booked || s == Confirmed || s == CheckedIn || s == InService
		wantTerm := s == Completed || s == CancelledByClient || s == CancelledByShop || s == NoShow
		if s.Holds() != wantHold || s.Terminal() != wantTerm {
			t.Errorf("%s holds=%v terminal=%v", s, s.Holds(), s.Terminal())
		}
	}
}

func TestTransitions_NoShowGrace(t *testing.T) {
	start := jan14(t, 10, 0)
	try := func(from State, grace int, now time.Time) error {
		p := DefaultPolicy()
		p.NoShowGraceMin = grace
		_, _, err := Transition(appt("a", "r1", from, start, 30, 0), TransitionInput{To: NoShow, Now: now}, p, counterIDs("e"))
		return err
	}
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	if !errors.Is(try(Booked, 15, at(15*60-1)), ErrNoShowTooEarly) {
		t.Fatal("10:14:59 should be too early")
	}
	if try(Booked, 15, at(15*60)) != nil || try(Confirmed, 15, at(15*60)) != nil {
		t.Fatal("10:15:00 allowed")
	}
	if !errors.Is(try(CheckedIn, 15, at(3600)), ErrTransitionNotAllowed) {
		t.Fatal("checked_in -> no_show is not allowed")
	}
	if !errors.Is(try(InService, 15, at(3600)), ErrTransitionNotAllowed) {
		t.Fatal("in_service -> no_show is not allowed")
	}
	if try(Booked, 0, at(0)) != nil {
		t.Fatal("grace 0")
	}
	if !errors.Is(try(Booked, 30, at(15*60)), ErrNoShowTooEarly) {
		t.Fatal("grace 30")
	}
	if !errors.Is(try(Booked, -1, at(3600)), ErrInvalidPolicy) {
		t.Fatal("negative grace")
	}
}

func TestTransitions_CompleteNeedsSale(t *testing.T) {
	a := appt("a", "r1", InService, jan14(t, 10, 0), 30, 0)
	for _, ref := range []string{"", "   "} {
		got, _, err := Transition(a, TransitionInput{To: Completed, SaleRef: ref}, DefaultPolicy(), counterIDs("e"))
		if !errors.Is(err, ErrSaleRefRequired) || got.State != InService {
			t.Fatalf("%q: %v", ref, err)
		}
	}
	got, _, err := Transition(a, TransitionInput{To: Completed, SaleRef: "sale-123"}, DefaultPolicy(), counterIDs("e"))
	if err != nil || got.SaleRef != "sale-123" {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestTransitions_LateCancel(t *testing.T) {
	start := time.Date(2026, 1, 14, 14, 0, 0, 0, time.UTC)
	rows := []struct {
		name string
		to   State
		now  time.Time
		hrs  int
		want bool
	}{
		{"24h exactly -> false", CancelledByClient, start.Add(-24 * time.Hour), 24, false},
		{"23h59m59s before -> true", CancelledByClient, start.Add(-24*time.Hour + time.Second), 24, true},
		{"24h+1s before -> false", CancelledByClient, start.Add(-24*time.Hour - time.Second), 24, false},
		{"after start -> true", CancelledByClient, start.Add(time.Minute), 24, true},
		{"cancelled_by_shop 1h before -> false", CancelledByShop, start.Add(-time.Hour), 24, false},
		{"window 0 -> false", CancelledByClient, start.Add(-time.Minute), 0, false},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			p := DefaultPolicy()
			p.LateCancelHours = r.hrs
			got, _, err := Transition(appt("a", "r1", Booked, start, 30, 0), TransitionInput{To: r.to, Now: r.now}, p, counterIDs("e"))
			if err != nil || got.LateCancel != r.want {
				t.Fatalf("%v late=%v", err, got.LateCancel)
			}
		})
	}
	p := DefaultPolicy()
	p.LateCancelHours = -1
	if _, _, err := Transition(appt("a", "r1", Booked, start, 30, 0), TransitionInput{To: CancelledByClient, Now: start}, p, counterIDs("e")); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal(err)
	}
}

func TestTransitions_WalkIn(t *testing.T) {
	now := jan14(t, 10, 0)
	a := appt("w1", "r1", Booked, now, 30, 0)
	w, ev, err := NewWalkIn(a, "u1", now, counterIDs("ev"))
	if err != nil {
		t.Fatal(err)
	}
	if w.State != CheckedIn || !w.WalkIn || w.Source != "own" || ev.FromState != "" || ev.ToState != CheckedIn ||
		ev.Kind != "state_change" || ev.ActorUserID != "u1" || ev.At != now || w.CreatedAt != now {
		t.Fatalf("%+v %+v", w, ev)
	}
	w, _, err = Transition(w, TransitionInput{To: InService, Now: now}, DefaultPolicy(), counterIDs("e"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = Transition(w, TransitionInput{To: Completed, SaleRef: "s", Now: now}, DefaultPolicy(), counterIDs("e")); err != nil {
		t.Fatal(err)
	}
	bad := a
	bad.ID = ""
	if _, _, err := NewWalkIn(bad, "u", now, counterIDs("e")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestTransitions_Supersede(t *testing.T) {
	old := appt("old", "r1", Confirmed, jan14(t, 10, 0), 30, 5)
	old.Lines = []Line{{SKU: "s", Amount: 100}}
	ns := jan14(t, 15, 0)
	n, err := Supersede(old, ns, "r2", "new", counterIDs("i"))
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "new" || n.SupersedesID != "old" || n.State != Confirmed || n.Start != ns ||
		n.End != ns.Add(30*time.Minute) || n.ResourceID != "r2" || n.BufferMin != 5 || len(n.Lines) != 1 {
		t.Fatalf("%+v", n)
	}
	n.Lines[0].SKU = "changed"
	if old.Lines[0].SKU != "s" {
		t.Fatal("lines must be copied")
	}
	for _, st := range []State{Completed, CancelledByClient, CancelledByShop, NoShow, InService} {
		o := old
		o.State = st
		if _, err := Supersede(o, ns, "r2", "new", counterIDs("i")); !errors.Is(err, ErrTransitionNotAllowed) {
			t.Errorf("%s: %v", st, err)
		}
	}
	if n, err := Supersede(old, ns, "r2", "", counterIDs("i")); err != nil || n.ID != "i1" {
		t.Fatalf("generated id: %v %v", n.ID, err)
	}
	if _, err := Supersede(old, ns, "", "new", counterIDs("i")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// combined with CheckConflict: the superseded row does not block the new one
	c := n
	c.ResourceID = "r1"
	c.Start, c.End = old.Start, old.End
	if _, err := CheckConflict(c, []Appointment{old}, DefaultPolicy(), nil, fakeClock{ns}, counterIDs("e")); err != nil {
		t.Fatal(err)
	}
}

func TestTransitions_NoShowUndo(t *testing.T) {
	start := jan14(t, 10, 0)
	now := start.Add(20 * time.Minute)
	a := appt("a", "r1", NoShow, start, 30, 0)
	rows := []struct {
		name string
		in   TransitionInput
		want error
	}{
		{"manager with reason", TransitionInput{To: CheckedIn, ActorUserID: "m1", Now: now, ByManager: true, Reason: "client arrived at 10:16"}, nil},
		{"not a manager", TransitionInput{To: CheckedIn, ActorUserID: "u1", Now: now, Reason: "x"}, ErrOverrideNotAllowed},
		{"empty reason", TransitionInput{To: CheckedIn, ActorUserID: "m1", Now: now, ByManager: true}, ErrReasonRequired},
		{"blank reason", TransitionInput{To: CheckedIn, ActorUserID: "m1", Now: now, ByManager: true, Reason: " \t "}, ErrReasonRequired},
		{"reason 501 runes", TransitionInput{To: CheckedIn, ActorUserID: "m1", Now: now, ByManager: true, Reason: strings.Repeat("é", 501)}, ErrInvalid},
		{"reason 500 runes", TransitionInput{To: CheckedIn, ActorUserID: "m1", Now: now, ByManager: true, Reason: strings.Repeat("é", 500)}, nil},
		{"no actor", TransitionInput{To: CheckedIn, Now: now, ByManager: true, Reason: "x"}, ErrInvalid},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			got, ev, err := Transition(a, r.in, DefaultPolicy(), counterIDs("e"))
			if r.want != nil {
				if !errors.Is(err, r.want) || got.State != NoShow || ev.ID != "" {
					t.Fatalf("want %v got %v %s %+v", r.want, err, got.State, ev)
				}
				return
			}
			if err != nil || got.State != CheckedIn || ev.Reason != strings.TrimSpace(r.in.Reason) ||
				ev.FromState != NoShow || ev.ToState != CheckedIn || ev.ActorUserID != r.in.ActorUserID {
				t.Fatalf("%v %s %+v", err, got.State, ev)
			}
		})
	}
}

func TestTransitions_ShopCancelInService(t *testing.T) {
	start := jan14(t, 10, 0)
	a := appt("a", "r1", InService, start, 30, 0)
	for _, reason := range []string{"", "  "} {
		got, ev, err := Transition(a, TransitionInput{To: CancelledByShop, ActorUserID: "u1", Now: start, Reason: reason}, DefaultPolicy(), counterIDs("e"))
		if !errors.Is(err, ErrReasonRequired) || got.State != InService || ev.ID != "" {
			t.Fatalf("%q: %v", reason, err)
		}
	}
	got, ev, err := Transition(a, TransitionInput{To: CancelledByShop, ActorUserID: "u1", Now: start, Reason: "wrong appointment"}, DefaultPolicy(), counterIDs("e"))
	if err != nil || got.State != CancelledByShop || ev.Reason != "wrong appointment" || got.LateCancel {
		t.Fatalf("%v %+v %+v", err, got, ev)
	}
	// from booked, a shop cancel keeps needing no reason.
	if _, _, err := Transition(appt("b", "r1", Booked, start, 30, 0), TransitionInput{To: CancelledByShop, ActorUserID: "u1", Now: start}, DefaultPolicy(), counterIDs("e")); err != nil {
		t.Fatal(err)
	}
}

func TestTransitions_UndoCheckIn(t *testing.T) {
	start := jan14(t, 10, 0)
	got, ev, err := Transition(appt("a", "r1", CheckedIn, start, 30, 0), TransitionInput{To: Booked, ActorUserID: "u1", Now: start}, DefaultPolicy(), counterIDs("e"))
	if err != nil || got.State != Booked || ev.FromState != CheckedIn || ev.ToState != Booked {
		t.Fatalf("%v %+v", err, ev)
	}
}
