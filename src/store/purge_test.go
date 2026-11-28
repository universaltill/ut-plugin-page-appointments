package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

func daysAfter(t time.Time, n int) time.Time { return t.Add(time.Duration(n) * 24 * time.Hour) }

func TestPurge_DefaultNinetyDays_BoundaryAndSuperseded(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		a := mustCreate(t, e, "r1", at(14, 10, 0), "Zephyrine")
		// reschedule: the superseded row carries the name too
		n, err := e.repo.Reschedule(ctx, a.ID, at(14, 15, 0), "r1", "u1", nil)
		if err != nil {
			t.Fatal(err)
		}
		end := n.End // 15:30 on the 14th
		e.clk.t = daysAfter(end, 90).Add(-time.Second)
		if r, err := e.repo.Purge(ctx); err != nil || r.Rows != 0 {
			t.Fatalf("one second early: %v %+v", err, r)
		}
		if !strings.Contains(e.dump(), "Zephyrine") {
			t.Fatal("name must still be stored before the period ends")
		}
		e.clk.t = daysAfter(end, 90)
		r, err := e.repo.Purge(ctx)
		if err != nil || r.Rows != 2 || r.Chains != 1 {
			t.Fatalf("%v %+v", err, r)
		}
		for _, id := range []string{a.ID, n.ID} {
			g, _ := e.repo.Get(ctx, id)
			if g.FirstName != "" {
				t.Fatalf("row %s still named", id)
			}
			// booking data untouched
			if g.Start.IsZero() || len(g.Lines) != 1 {
				t.Fatalf("%+v", g)
			}
		}
		evs, _ := e.repo.Events(ctx, n.ID)
		last := evs[len(evs)-1]
		if last.Kind != store.KindPurge || last.Reason != store.ReasonRetention || last.FromState != last.ToState {
			t.Fatalf("%+v", last)
		}
		if r, _ := e.repo.Purge(ctx); r.Rows != 0 {
			t.Fatalf("second purge is a no-op: %+v", r)
		}
		if evs2, _ := e.repo.Events(ctx, n.ID); len(evs2) != len(evs) {
			t.Fatal("idempotent purge wrote another event")
		}
	})
}

func TestPurge_RetentionSettingAndFuture(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		s := store.DefaultSettings()
		s.RetentionDays = 730
		if err := e.repo.SaveSettings(ctx, s); err != nil {
			t.Fatal(err)
		}
		past := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		e.clk.t = daysAfter(past.End, 729)
		if r, _ := e.repo.Purge(ctx); r.Rows != 0 {
			t.Fatalf("729 days: %+v", r)
		}
		e.clk.t = daysAfter(past.End, 730)
		if r, _ := e.repo.Purge(ctx); r.Rows != 1 {
			t.Fatalf("730 days: %+v", r)
		}
		// a visit that has not happened yet is never purged
		fut := mustCreate(t, e, "r1", daysAfter(e.clk.t, 5), "Bea")
		if r, _ := e.repo.Purge(ctx); r.Rows != 0 {
			t.Fatalf("future: %+v", r)
		}
		if g, _ := e.repo.Get(ctx, fut.ID); g.FirstName != "Bea" {
			t.Fatal("future booking lost its name")
		}
	})
}

func TestPurge_CancelledUsesCancellationTime(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		// booked for 60 days ahead, cancelled today: the clock starts at the cancellation
		far := mustCreate(t, e, "r1", daysAfter(at(14, 10, 0), 60), "Ann")
		if _, err := e.repo.Transition(ctx, far.ID, engine.TransitionInput{To: engine.CancelledByShop}); err != nil {
			t.Fatal(err)
		}
		cancelledAt := e.clk.t
		e.clk.t = daysAfter(cancelledAt, 89)
		if r, _ := e.repo.Purge(ctx); r.Rows != 0 {
			t.Fatalf("%+v", r)
		}
		e.clk.t = daysAfter(cancelledAt, 90)
		if r, _ := e.repo.Purge(ctx); r.Rows != 1 {
			t.Fatalf("90 days after cancellation: %+v", r)
		}
	})
}

func TestErase_Immediate(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		a := mustCreate(t, e, "r1", daysAfter(at(14, 10, 0), 3), "Quillon")
		n, err := e.repo.Reschedule(ctx, a.ID, daysAfter(at(14, 11, 0), 3), "r1", "u1", nil)
		if err != nil {
			t.Fatal(err)
		}
		other := mustCreate(t, e, "r2", at(14, 10, 0), "Bystander")
		// erase by the OLD row id: the whole chain is blanked
		cnt, err := e.repo.Erase(ctx, a.ID, "owner1")
		if err != nil || cnt != 2 {
			t.Fatalf("%v %d", err, cnt)
		}
		for _, id := range []string{a.ID, n.ID} {
			if g, _ := e.repo.Get(ctx, id); g.FirstName != "" {
				t.Fatalf("%s: %q", id, g.FirstName)
			}
		}
		if g, _ := e.repo.Get(ctx, other.ID); g.FirstName != "Bystander" {
			t.Fatal("unrelated booking touched")
		}
		evs, _ := e.repo.Events(ctx, n.ID)
		last := evs[len(evs)-1]
		if last.Kind != store.KindPurge || last.Reason != store.ReasonErasure || last.ActorUserID != "owner1" {
			t.Fatalf("%+v", last)
		}
		if _, err := e.repo.Erase(ctx, "nope", "o"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		if cnt, _ := e.repo.Erase(ctx, n.ID, "o"); cnt != 0 {
			t.Fatal("second erase blanks nothing")
		}
	})
}

// PII test: after the purge no name is anywhere in the stored data, on any
// backend, and the data that must stay (times, lines, events) is still there.
func TestPII_DumpAfterPurgeHasNoNames(t *testing.T) {
	names := []string{"Zephyrine", "Quillon", "Marigold", "Thaddeus"}
	forEachBackend(t, func(t *testing.T, e *env) {
		s := store.DefaultSettings()
		s.AllowDoubleBooking = true
		_ = e.repo.SaveSettings(ctx, s)
		a := mustCreate(t, e, "r1", at(14, 10, 0), names[0])
		if _, err := e.repo.Reschedule(ctx, a.ID, at(14, 12, 0), "r1", "u1", nil); err != nil {
			t.Fatal(err)
		}
		b := mustCreate(t, e, "r2", at(14, 10, 0), names[1])
		if _, err := e.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.CancelledByClient}); err != nil {
			t.Fatal(err)
		}
		// an override booking, and a walk-in with a name
		_, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 12, 15), 30, 0), FirstName: names[2], ActorUserID: "m1",
			Override: &engine.Override{ByManager: true, Reason: "regular client", ActorUserID: "m1"}})
		if err != nil {
			t.Fatal(err)
		}
		w := draft("r3", at(14, 9, 0), 30, 0)
		w.WalkIn = true
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: w, FirstName: names[3], ActorUserID: "u1"}); err != nil {
			t.Fatal(err)
		}
		before := e.dump()
		for _, n := range names {
			if !strings.Contains(before, n) {
				t.Fatalf("sanity: %s should be stored before the purge", n)
			}
		}
		e.clk.t = at(14, 8, 0).Add(91 * 24 * time.Hour)
		r, err := e.repo.Purge(ctx)
		if err != nil || r.Rows != 5 {
			t.Fatalf("%v %+v", err, r)
		}
		after := e.dump()
		for _, n := range names {
			if strings.Contains(after, n) {
				t.Errorf("%q survived the purge", n)
			}
		}
		// what stays: lines and events
		for _, keep := range []string{"cut", "regular client", "conflict_override"} {
			if !strings.Contains(after, keep) {
				t.Errorf("%q should remain", keep)
			}
		}
		// no phone, email or note shaped data exists at all
		for _, bad := range []string{"@", "phone", "email", "note"} {
			if strings.Contains(strings.ToLower(after), bad) {
				t.Errorf("dump contains %q", bad)
			}
		}
	})
}

func TestValidateFirstName(t *testing.T) {
	ok := []string{"", "Ann", "  Zoë  ", "Anne-Marie", "O'Neil", "Łukasz", "María José", "李", strings.Repeat("a", 40), strings.Repeat("é", 40)}
	for _, s := range ok {
		if _, err := store.ValidateFirstName(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if g, _ := store.ValidateFirstName("  Zoë  "); g != "Zoë" {
		t.Fatalf("%q", g)
	}
	cases := map[string]error{
		strings.Repeat("a", 41): store.ErrNameTooLong, strings.Repeat("é", 41): store.ErrNameTooLong,
		"Ann1": store.ErrNameChars, "07700900123": store.ErrNameChars, "a@b": store.ErrNameChars, "@": store.ErrNameChars,
		"٣": store.ErrNameChars, "²": store.ErrNameChars,
	}
	for s, want := range cases {
		if _, err := store.ValidateFirstName(s); !errors.Is(err, want) {
			t.Errorf("%q: %v want %v", s, err, want)
		}
	}
	if _, err := store.ValidateFirstName("An\x00n"); !errors.Is(err, store.ErrNameChars) {
		t.Error("control character")
	}
}

func TestOtherValidators(t *testing.T) {
	for _, r := range store.TimeOffReasons {
		if g, err := store.NormaliseTimeOffReason(r); err != nil || g != r {
			t.Fatal(r, err)
		}
	}
	if g, _ := store.NormaliseTimeOffReason(""); g != "other" {
		t.Fatal(g)
	}
	if _, err := store.NormaliseTimeOffReason("sick"); !errors.Is(err, store.ErrTimeOffReason) {
		t.Fatal(err)
	}
	for n, ok := range map[int]bool{0: false, 1: true, 90: true, 730: true, 731: false, -5: false} {
		if (store.ValidateRetentionDays(n) == nil) != ok {
			t.Errorf("retention %d", n)
		}
	}
	if store.OverrideReasonHint != "Do not write client or health details" || store.DefaultRetentionDays != 90 || store.MaxRetentionDays != 730 {
		t.Fatal("binding constants")
	}
}

// No note field on bookings: the types carry no note, phone or email.
func TestNoNoteOrContactFieldsOnTypes(t *testing.T) {
	for _, v := range []any{store.Booking{}, store.NewBooking{}, engine.Appointment{}} {
		rt := reflectType(v)
		for i := 0; i < rt.NumField(); i++ {
			n := strings.ToLower(rt.Field(i).Name)
			for _, bad := range []string{"note", "phone", "email", "comment", "mobile"} {
				if strings.Contains(n, bad) {
					t.Errorf("%s has field %s", rt.Name(), rt.Field(i).Name)
				}
			}
		}
	}
}
