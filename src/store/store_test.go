package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
	"github.com/universaltill/ut-plugin-page-appointments/src/store/memdb"
)

var ctx = context.Background()

func TestMigrations_LoadAndShape(t *testing.T) {
	ms, err := store.Migrations()
	if err != nil || len(ms) != 2 || ms[0].Name != "schema" || ms[1].Name != "appointments" {
		t.Fatalf("%v %+v", err, ms)
	}
	if len(ms[0].Checksum()) != 64 || ms[0].Checksum() == ms[1].Checksum() {
		t.Fatal("checksums")
	}
}

// A DB dump contains no phone, email or note column (acceptance criterion).
func TestMigrations_NoContactOrNoteColumns(t *testing.T) {
	ms, _ := store.Migrations()
	for _, m := range ms {
		low := strings.ToLower(m.SQL)
		for _, bad := range []string{"phone", "email", "e_mail", "note", "mobile", "address", "comment", "has_note"} {
			if strings.Contains(low, bad) {
				t.Errorf("migration %s mentions %q", m.Name, bad)
			}
		}
	}
}

func TestMigrations_ApplyAndRollForward_SQLite(t *testing.T) {
	c := newCLIDB(t)
	ms, _ := store.Migrations()
	if err := c.script(ms[0].SQL); err != nil {
		t.Fatal(err)
	}
	// data written under version 1 survives version 2
	if _, err := c.Exec(ctx, store.SQLUpsertResource, "r1", "chair", "Chair 1", "", "active", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.script(ms[1].SQL); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Query(ctx, store.SQLListResources)
	if err != nil || !strings.Contains(string(rows), `"Chair 1"`) {
		t.Fatalf("%s %v", rows, err)
	}
	// applying a migration twice fails (the host ledger prevents it)
	if err := c.script(ms[0].SQL); err == nil {
		t.Fatal("expected failure on re-apply")
	}
}

// The schema itself refuses edits of booking data, deletes, and event edits.
func TestSchema_ImmutabilityTriggers_SQLite(t *testing.T) {
	c := newCLIDB(t)
	ms, _ := store.Migrations()
	for _, m := range ms {
		if err := c.script(m.SQL); err != nil {
			t.Fatal(err)
		}
	}
	_, err := c.Exec(ctx, store.SQLInsertAppointment, "a1", "t", "own", "r1", 1000, 2800, 0, 0, "booked", "Ann", "GBP", "", 0, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, store.SQLInsertEvent, "e1", "a1", 1, "u", "state_change", "", "booked", "", "[]"); err != nil {
		t.Fatal(err)
	}
	refused := []string{
		`UPDATE appointments SET start_utc = 5 WHERE id = 'a1'`,
		`UPDATE appointments SET resource_id = 'r2' WHERE id = 'a1'`,
		`UPDATE appointments SET client_first_name = 'Bob' WHERE id = 'a1'`,
		`DELETE FROM appointments WHERE id = 'a1'`,
		`UPDATE appointment_events SET reason = 'x' WHERE id = 'e1'`,
		`DELETE FROM appointment_events WHERE id = 'e1'`,
		`INSERT INTO appointment_events (id, appointment_id, at, kind) VALUES ('e2', 'a1', 1, 'conflict_override')`, // no actor
		`INSERT INTO time_off (id, resource_id, from_utc, to_utc, reason) VALUES ('t', 'r1', 1, 2, 'sick leave')`,   // free text reason
		`INSERT INTO appointments (id, origin_till_id, source, resource_id, start_utc, end_utc, buffer_min, walk_in, state, client_first_name, currency, created_at) VALUES ('a9','t','own','r1',1,2,0,0,'booked','` + strings.Repeat("a", 41) + `','GBP',1)`,
	}
	for _, q := range refused {
		if err := c.script(q + ";"); err == nil {
			t.Errorf("schema accepted: %s", q)
		}
	}
	// allowed: state projection and blanking the name
	for _, q := range []string{
		`UPDATE appointments SET state = 'confirmed' WHERE id = 'a1'`,
		`UPDATE appointments SET client_first_name = '' WHERE id = 'a1'`,
	} {
		if err := c.script(q + ";"); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
}

func TestResourcesHoursTimeOff_RoundTrip(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		if err := e.repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff", Active: true},
			DisplayName: " Sam ", Colour: "#fa0", LogbookStaffRef: "st-1"}); err != nil {
			t.Fatal(err)
		}
		hours := map[time.Weekday][]engine.Window{time.Monday: {{StartMin: 540, EndMin: 720}, {StartMin: 780, EndMin: 1020}}, time.Sunday: {{StartMin: 600, EndMin: 700}}}
		if err := e.repo.SetWorkingHours(ctx, "r1", hours); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.AddTimeOff(ctx, "r1", at(20, 0, 0), at(22, 0, 0), "holiday"); err != nil {
			t.Fatal(err)
		}
		got, err := e.repo.Resource(ctx, "r1")
		if err != nil {
			t.Fatal(err)
		}
		if got.DisplayName != "Sam" || got.Kind != "staff" || !got.Active || got.Colour != "#fa0" || got.LogbookStaffRef != "st-1" ||
			!reflect.DeepEqual(got.WorkingHours, hours) || len(got.TimeOff) != 1 || got.TimeOff[0].Reason != "holiday" ||
			!got.TimeOff[0].From.Equal(at(20, 0, 0)) {
			t.Fatalf("%+v", got)
		}
		// replace hours
		if err := e.repo.SetWorkingHours(ctx, "r1", map[time.Weekday][]engine.Window{time.Friday: {{StartMin: 0, EndMin: 1440}}}); err != nil {
			t.Fatal(err)
		}
		got, _ = e.repo.Resource(ctx, "r1")
		if len(got.WorkingHours) != 1 || len(got.WorkingHours[time.Friday]) != 1 {
			t.Fatalf("%+v", got.WorkingHours)
		}
		// deactivate (upsert)
		_ = e.repo.SaveResource(ctx, store.ResourceRecord{Resource: engine.Resource{ID: "r1", Kind: "staff"}, DisplayName: "Sam"})
		if got, _ = e.repo.Resource(ctx, "r1"); got.Active {
			t.Fatal("still active")
		}
		if _, err := e.repo.Resource(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		// delete time off
		tos, _ := e.repo.TimeOff(ctx, "r1")
		if err := e.repo.DeleteTimeOff(ctx, tos[0].ID); err != nil {
			t.Fatal(err)
		}
		if tos, _ = e.repo.TimeOff(ctx, ""); len(tos) != 0 {
			t.Fatal(tos)
		}
	})
}

func TestResourceValidation(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		bad := []store.ResourceRecord{
			{Resource: engine.Resource{ID: "", Kind: "chair"}, DisplayName: "x"},
			{Resource: engine.Resource{ID: "r", Kind: "room"}, DisplayName: "x"},
			{Resource: engine.Resource{ID: "r", Kind: "chair"}, DisplayName: "  "},
			{Resource: engine.Resource{ID: "r", Kind: "chair"}, DisplayName: strings.Repeat("x", 81)},
		}
		for i, r := range bad {
			if err := e.repo.SaveResource(ctx, r); !errors.Is(err, store.ErrInvalidInput) {
				t.Errorf("%d: %v", i, err)
			}
		}
		for i, h := range []map[time.Weekday][]engine.Window{
			{time.Monday: {{StartMin: 600, EndMin: 600}}},
			{time.Monday: {{StartMin: 600, EndMin: 1441}}},
			{time.Monday: {{StartMin: -1, EndMin: 10}}},
			{time.Monday: {{StartMin: 540, EndMin: 700}, {StartMin: 600, EndMin: 800}}},
		} {
			if err := e.repo.SetWorkingHours(ctx, "r1", h); !errors.Is(err, store.ErrInvalidInput) {
				t.Errorf("hours %d: %v", i, err)
			}
		}
		if err := e.repo.SetWorkingHours(ctx, "", nil); !errors.Is(err, store.ErrInvalidInput) {
			t.Error("empty resource id")
		}
		// time-off reason is a fixed list
		for _, r := range []string{"sick", "chemo", "Holiday"} {
			if _, err := e.repo.AddTimeOff(ctx, "r1", at(1, 0, 0), at(2, 0, 0), r); !errors.Is(err, store.ErrTimeOffReason) {
				t.Errorf("reason %q: %v", r, err)
			}
		}
		if _, err := e.repo.AddTimeOff(ctx, "r1", at(2, 0, 0), at(1, 0, 0), "away"); !errors.Is(err, store.ErrInvalidInput) {
			t.Error("reversed range")
		}
		id, err := e.repo.AddTimeOff(ctx, "r1", at(1, 0, 0), at(2, 0, 0), "")
		if err != nil || id == "" {
			t.Fatal(err)
		}
		if tos, _ := e.repo.TimeOff(ctx, "r1"); tos[0].Reason != "other" {
			t.Fatalf("empty reason is other: %+v", tos)
		}
	})
}

func TestServices_RoundTrip(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		for _, s := range []engine.Service{
			{SKU: "cut", DurationMin: 30, BufferMin: 5, ResourceIDs: []string{"r2", "r1", "r1"}, Active: true},
			{SKU: "dye", DurationMin: 90, All: true, Active: false},
		} {
			if err := e.repo.SaveService(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		got, err := e.repo.Services(ctx)
		want := []engine.Service{
			{SKU: "cut", DurationMin: 30, BufferMin: 5, ResourceIDs: []string{"r1", "r2"}, Active: true},
			{SKU: "dye", DurationMin: 90, All: true},
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%v\n got %+v\nwant %+v", err, got, want)
		}
		// switching to all clears the eligible list
		_ = e.repo.SaveService(ctx, engine.Service{SKU: "cut", DurationMin: 30, All: true, Active: true})
		got, _ = e.repo.Services(ctx)
		if got[0].ResourceIDs != nil || !got[0].All {
			t.Fatalf("%+v", got[0])
		}
		for _, s := range []engine.Service{{SKU: "", DurationMin: 5}, {SKU: "x", DurationMin: 0}, {SKU: "x", DurationMin: 5, BufferMin: -1}} {
			if err := e.repo.SaveService(ctx, s); !errors.Is(err, store.ErrInvalidInput) {
				t.Errorf("%+v: %v", s, err)
			}
		}
	})
}

func TestSettings_DefaultsAndRoundTrip(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		s, err := e.repo.Settings(ctx)
		if err != nil || !reflect.DeepEqual(s, store.DefaultSettings()) || s.RetentionDays != 90 {
			t.Fatalf("%v %+v", err, s)
		}
		s.SlotStepMin, s.AllowDoubleBooking, s.RetentionDays, s.ShopZone, s.LateCancelHours = 30, true, 730, "Europe/Dublin", 0
		if err := e.repo.SaveSettings(ctx, s); err != nil {
			t.Fatal(err)
		}
		got, _ := e.repo.Settings(ctx)
		if !reflect.DeepEqual(got, s) {
			t.Fatalf("%+v want %+v", got, s)
		}
		for name, mut := range map[string]func(*store.Settings){
			"step":      func(s *store.Settings) { s.SlotStepMin = 7 },
			"ret 0":     func(s *store.Settings) { s.RetentionDays = 0 },
			"ret 731":   func(s *store.Settings) { s.RetentionDays = 731 },
			"zone":      func(s *store.Settings) { s.ShopZone = "Mars/Base" },
			"currency":  func(s *store.Settings) { s.Currency = "POUND" },
			"neg grace": func(s *store.Settings) { s.NoShowGraceMin = -1 },
		} {
			b := store.DefaultSettings()
			mut(&b)
			if err := e.repo.SaveSettings(ctx, b); err == nil {
				t.Errorf("%s accepted", name)
			}
		}
		if loc, err := got.Location(); err != nil || loc.String() != "Europe/Dublin" {
			t.Fatal(err)
		}
	})
}

func TestCreate_RoundTripAndEvents(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		b := mustCreate(t, e, "r1", at(14, 10, 0), "  Ann ")
		g, err := e.repo.Get(ctx, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if g.FirstName != "Ann" || g.State != engine.Booked || g.Source != "own" || g.OriginTillID != "till1" || g.Currency != "GBP" ||
			!g.Start.Equal(at(14, 10, 0)) || !g.End.Equal(at(14, 10, 30)) || !g.CreatedAt.Equal(at(14, 8, 0)) ||
			len(g.Lines) != 1 || g.Lines[0].Amount.Minor() != 2500 || g.Lines[0].Name != "Cut" || g.WalkIn {
			t.Fatalf("%+v", g)
		}
		evs, _ := e.repo.Events(ctx, b.ID)
		if len(evs) != 1 || evs[0].ToState != engine.Booked || evs[0].FromState != "" || evs[0].ActorUserID != "u1" {
			t.Fatalf("%+v", evs)
		}
		if _, err := e.repo.Get(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	})
}

func TestCreate_Walkin(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		d := draft("r1", at(14, 8, 0), 30, 0)
		d.WalkIn = true
		b, err := e.repo.Create(ctx, store.NewBooking{Appointment: d, ActorUserID: "u1"})
		if err != nil || b.State != engine.CheckedIn || !b.WalkIn || b.FirstName != "" {
			t.Fatalf("%v %+v", err, b)
		}
	})
}

func TestCreate_Validation(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		cases := map[string]struct {
			nb   store.NewBooking
			want error
		}{
			"digit":       {store.NewBooking{Appointment: draft("r1", at(14, 9, 0), 30, 0), FirstName: "Ann2"}, store.ErrNameChars},
			"at":          {store.NewBooking{Appointment: draft("r1", at(14, 9, 0), 30, 0), FirstName: "a@b"}, store.ErrNameChars},
			"long":        {store.NewBooking{Appointment: draft("r1", at(14, 9, 0), 30, 0), FirstName: strings.Repeat("a", 41)}, store.ErrNameTooLong},
			"no lines":    {store.NewBooking{Appointment: engine.Appointment{ResourceID: "r1", Start: at(14, 9, 0), End: at(14, 9, 30)}}, store.ErrInvalidInput},
			"bad times":   {store.NewBooking{Appointment: engine.Appointment{ResourceID: "r1", Start: at(14, 9, 0), End: at(14, 9, 0), Lines: []engine.Line{line("c", 5, 0)}}}, engine.ErrInvalid},
			"no resource": {store.NewBooking{Appointment: engine.Appointment{Start: at(14, 9, 0), End: at(14, 9, 30), Lines: []engine.Line{line("c", 5, 0)}}}, engine.ErrInvalid},
		}
		for n, c := range cases {
			if _, err := e.repo.Create(ctx, c.nb); !errors.Is(err, c.want) {
				t.Errorf("%s: %v want %v", n, err, c.want)
			}
		}
		dep := draft("r1", at(14, 9, 0), 30, 0)
		dep.Deposit = 500
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: dep}); !errors.Is(err, store.ErrDepositReserved) {
			t.Errorf("deposit: %v", err)
		}
		bad := draft("r1", at(14, 9, 0), 30, 0)
		bad.Lines[0].Qty = 0
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: bad}); !errors.Is(err, store.ErrInvalidInput) {
			t.Errorf("qty: %v", err)
		}
		if cur, _ := e.repo.Current(ctx, at(14, 0, 0), at(15, 0, 0), ""); len(cur) != 0 {
			t.Fatalf("a refused booking was stored: %+v", cur)
		}
	})
}

func TestCreate_ConflictAndOverride(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		first := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		_, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 15), 30, 0), FirstName: "Bea"})
		if !errors.Is(err, engine.ErrConflict) {
			t.Fatalf("%v", err)
		}
		// other resource is free
		mustCreate(t, e, "r2", at(14, 10, 15), "Bea")
		// override needs the policy on, a manager, a reason and an actor
		ov := &engine.Override{ByManager: true, Reason: "regular client", ActorUserID: "m1"}
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 15), 30, 0), Override: ov}); !errors.Is(err, engine.ErrOverrideNotAllowed) {
			t.Fatalf("%v", err)
		}
		s := store.DefaultSettings()
		s.AllowDoubleBooking = true
		_ = e.repo.SaveSettings(ctx, s)
		noActor := &engine.Override{ByManager: true, Reason: "regular client"}
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 15), 30, 0), Override: noActor}); !errors.Is(err, engine.ErrInvalid) {
			t.Fatalf("override without actor: %v", err)
		}
		b, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 15), 30, 0), FirstName: "Cy", Override: ov, ActorUserID: "m1"})
		if err != nil {
			t.Fatal(err)
		}
		evs, _ := e.repo.Events(ctx, b.ID)
		if len(evs) != 2 || evs[1].Kind != engine.KindConflictOverride || evs[1].Reason != "regular client" ||
			evs[1].ActorUserID != "m1" || !reflect.DeepEqual(evs[1].ConflictingIDs, []string{first.ID}) {
			t.Fatalf("%+v", evs)
		}
	})
}

// F11 at repository level: reschedule, then a third booking on the old slot.
func TestReschedule_FreesOldSlot(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		a := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		e.clk.t = at(14, 8, 30)
		n, err := e.repo.Reschedule(ctx, a.ID, at(14, 14, 0), "r1", "u1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n.SupersedesID != a.ID || n.ID == a.ID || n.FirstName != "Ann" || n.State != engine.Booked ||
			!n.CreatedAt.Equal(at(14, 8, 30)) || !n.End.Equal(at(14, 14, 30)) || len(n.Lines) != 1 {
			t.Fatalf("%+v", n)
		}
		// the old row is unchanged and still readable
		old, _ := e.repo.Get(ctx, a.ID)
		if !old.Start.Equal(at(14, 10, 0)) || old.State != engine.Booked {
			t.Fatalf("old row changed: %+v", old)
		}
		// F11: a third booking takes the old slot
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Bea"}); err != nil {
			t.Fatalf("old slot must be free: %v", err)
		}
		// and the new slot is held
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 14, 0), 30, 0)}); !errors.Is(err, engine.ErrConflict) {
			t.Fatalf("%v", err)
		}
		// the superseded row takes no further transition or reschedule
		if _, err := e.repo.Transition(ctx, a.ID, engine.TransitionInput{To: engine.Confirmed}); !errors.Is(err, store.ErrNotCurrent) {
			t.Fatalf("%v", err)
		}
		if _, err := e.repo.Reschedule(ctx, a.ID, at(14, 16, 0), "r1", "u1", nil); !errors.Is(err, store.ErrNotCurrent) {
			t.Fatalf("%v", err)
		}
		// current list: Bea 10:00 and the moved Ann
		cur, _ := e.repo.Current(ctx, at(14, 0, 0), at(15, 0, 0), "r1")
		if len(cur) != 2 || cur[0].FirstName != "Bea" || cur[1].ID != n.ID {
			t.Fatalf("%+v", cur)
		}
		// feeding Current rows to the engine: Ann can be moved 15 minutes (F13)
		ex := make([]engine.Appointment, len(cur))
		for i, c := range cur {
			ex[i] = c.Appointment
		}
		sl, err := engine.AvailabilityExcluding(time.UTC, engine.Date{Y: 2026, M: time.January, D: 14},
			engine.Resource{ID: "r1", Active: true, WorkingHours: allDay()}, engine.Request{Services: []engine.Service{{SKU: "cut", All: true, Active: true}}, Lines: []engine.Line{line("cut", 30, 0)}},
			ex, n.ID, store.DefaultSettings().Policy, at(14, 8, 0))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, s := range sl {
			found = found || s.Start.Equal(at(14, 14, 15))
		}
		if !found {
			t.Fatal("14:15 should be offered when moving the 14:00 booking")
		}
	})
}

func allDay() map[time.Weekday][]engine.Window {
	m := map[time.Weekday][]engine.Window{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		m[d] = []engine.Window{{StartMin: 540, EndMin: 1080}}
	}
	return m
}

func TestReschedule_ConflictAndErrors(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		a := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		mustCreate(t, e, "r1", at(14, 12, 0), "Bea")
		if _, err := e.repo.Reschedule(ctx, a.ID, at(14, 12, 15), "r1", "u1", nil); !errors.Is(err, engine.ErrConflict) {
			t.Fatalf("%v", err)
		}
		// a failed reschedule leaves the original holding its slot
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0)}); !errors.Is(err, engine.ErrConflict) {
			t.Fatalf("%v", err)
		}
		// moving by 15 minutes onto its own slot is allowed (own row ignored)
		if _, err := e.repo.Reschedule(ctx, a.ID, at(14, 10, 15), "r1", "u1", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.Reschedule(ctx, "nope", at(14, 10, 15), "r1", "u1", nil); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%v", err)
		}
		b := mustCreate(t, e, "r2", at(14, 9, 0), "Cy")
		if _, err := e.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.CancelledByShop}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.Reschedule(ctx, b.ID, at(14, 9, 30), "r2", "u1", nil); !errors.Is(err, engine.ErrTransitionNotAllowed) {
			t.Fatalf("a cancelled booking cannot be rescheduled: %v", err)
		}
	})
}

func TestTransition_EventsAndProjection(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		b := mustCreate(t, e, "r1", at(14, 10, 0), "Ann")
		e.clk.t = at(14, 9, 0)
		got, err := e.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.CancelledByClient, ActorUserID: "u2"})
		if err != nil || got.State != engine.CancelledByClient || !got.LateCancel {
			t.Fatalf("%v %+v", err, got)
		}
		g, _ := e.repo.Get(ctx, b.ID)
		if g.State != engine.CancelledByClient || !g.LateCancel {
			t.Fatalf("%+v", g)
		}
		evs, _ := e.repo.Events(ctx, b.ID)
		if len(evs) != 2 || evs[1].FromState != engine.Booked || evs[1].ToState != engine.CancelledByClient || evs[1].ActorUserID != "u2" {
			t.Fatalf("%+v", evs)
		}
		// a cancelled booking frees the chair
		if _, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0)}); err != nil {
			t.Fatal(err)
		}
		// engine refusals pass through and write nothing
		if _, err := e.repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.Confirmed}); !errors.Is(err, engine.ErrTransitionNotAllowed) {
			t.Fatalf("%v", err)
		}
		if evs, _ = e.repo.Events(ctx, b.ID); len(evs) != 2 {
			t.Fatal("event written on refusal")
		}
		c := mustCreate(t, e, "r2", at(14, 11, 0), "Cy")
		if _, err := e.repo.Transition(ctx, c.ID, engine.TransitionInput{To: engine.CheckedIn}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.Transition(ctx, c.ID, engine.TransitionInput{To: engine.InService}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.Transition(ctx, c.ID, engine.TransitionInput{To: engine.Completed}); !errors.Is(err, engine.ErrSaleRefRequired) {
			t.Fatalf("%v", err)
		}
		done, err := e.repo.Transition(ctx, c.ID, engine.TransitionInput{To: engine.Completed, SaleRef: "sale-9"})
		if err != nil || done.SaleRef != "sale-9" {
			t.Fatalf("%v %+v", err, done)
		}
		if g, _ = e.repo.Get(ctx, c.ID); g.SaleRef != "sale-9" || g.State != engine.Completed {
			t.Fatalf("%+v", g)
		}
		if _, err := e.repo.Transition(ctx, "nope", engine.TransitionInput{To: engine.Confirmed}); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("%v", err)
		}
	})
}

func TestTransition_StaleProjectionAndReconcile(t *testing.T) {
	d := newMem()
	clk := &clock{t: at(14, 8, 0)}
	repo := store.New(d, clk, counter("id"), "till1")
	b, err := repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Ann", ActorUserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	// crash after the projection moved, before the event is written
	d.FailExec = func(q string, _ []any) error {
		if q == store.SQLInsertEvent {
			return errors.New("disk full")
		}
		return nil
	}
	if _, err := repo.Transition(ctx, b.ID, engine.TransitionInput{To: engine.Confirmed, ActorUserID: "u1"}); err == nil {
		t.Fatal("expected failure")
	}
	d.FailExec = nil
	g, _ := repo.Get(ctx, b.ID)
	if g.State != engine.Confirmed {
		t.Fatalf("projection %v", g.State)
	}
	if evs, _ := repo.Events(ctx, b.ID); len(evs) != 1 {
		t.Fatalf("%+v", evs)
	}
	n, err := repo.Reconcile(ctx)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	evs, _ := repo.Events(ctx, b.ID)
	if len(evs) != 2 || evs[1].FromState != engine.Booked || evs[1].ToState != engine.Confirmed || evs[1].Reason != store.ReasonReconcile {
		t.Fatalf("%+v", evs)
	}
	if n, _ := repo.Reconcile(ctx); n != 0 {
		t.Fatal("reconcile is idempotent")
	}
}

func TestCreate_CrashBeforeRowLeavesNothingVisible(t *testing.T) {
	d := newMem()
	repo := store.New(d, &clock{t: at(14, 8, 0)}, counter("id"), "till1")
	d.FailExec = func(q string, _ []any) error {
		if q == store.SQLInsertAppointment {
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
	if cur, _ := repo.Current(ctx, at(14, 0, 0), at(15, 0, 0), ""); len(cur) != 0 {
		t.Fatalf("partial booking visible: %+v", cur)
	}
	// the retry with the same id is idempotent for the orphan lines
	if _, err := repo.Create(ctx, nb); err != nil {
		t.Fatal(err)
	}
	g, _ := repo.Get(ctx, "fixed")
	if len(g.Lines) != 1 {
		t.Fatalf("%+v", g.Lines)
	}
}

func TestConcurrentCreates_OneWins(t *testing.T) {
	forEachBackend(t, func(t *testing.T, e *env) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok, conflicts := 0, 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.repo.Create(ctx, store.NewBooking{Appointment: draft("r1", at(14, 10, 0), 30, 0), FirstName: "Ann"})
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					ok++
				} else if errors.Is(err, engine.ErrConflict) {
					conflicts++
				} else {
					t.Errorf("%v", err)
				}
			}()
		}
		wg.Wait()
		if ok != 1 || conflicts != 7 {
			t.Fatalf("ok=%d conflicts=%d", ok, conflicts)
		}
	})
}

func newMem() *memdb.DB { return memdb.New() }
