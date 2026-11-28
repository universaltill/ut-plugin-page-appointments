package calendar

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

var now8 = func() time.Time { return time.Date(2026, 1, 14, 8, 0, 0, 0, time.UTC) }

func createForm(staff, svc, date, name, tm string) map[string]any {
	return map[string]any{"staff": staff, "service": svc, "date": date, "first_name": name, "time": tm}
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func TestCreate_FindThenBook(t *testing.T) {
	f := newFake(t)
	d := newDeps(f)
	now := now8()
	find := run(t, d, ask("find", now, cashier, createForm("st1", "cut", "2026-01-14", " Ana ", "")))
	has(t, find.Document, "Free times with Ana on Wed 2026-01-14")
	start := jan14(t, 10, 0)
	ans := run(t, d, ask("create", now, cashier, createForm("st1", "cut", "2026-01-14", "Ana", iso(start))))
	has(t, ans.Document, "Booked Ana with Ana at 10:00.")
	if len(f.rows) != 1 {
		t.Fatal(f.rows)
	}
	var b Booking
	for _, b = range f.rows {
	}
	if b.State != engine.Booked || b.Source != "own" || !b.Start.Equal(start) || b.End != start.Add(30*time.Minute) ||
		b.BufferMin != 5 || b.FirstName != "Ana" || b.OriginTillID != "till-main" || len(b.Lines) != 1 || b.Lines[0].Amount != 2500 {
		t.Fatalf("%+v", b)
	}
	if len(f.events) != 1 || f.events[0].ToState != engine.Booked || f.events[0].ActorUserID != "u-cash" || f.events[0].FromState != "" {
		t.Fatalf("%+v", f.events)
	}
	// the day view now lists it
	has(t, ans.Document, "10:00-10:30")
}

func TestCreate_NameRulesAndFieldErrors(t *testing.T) {
	cases := []struct {
		name string
		form map[string]any
		want string
	}{
		{"digit", createForm("st1", "cut", "2026-01-14", "Anna 2", ""), "First name only: no digits"},
		{"at sign", createForm("st1", "cut", "2026-01-14", "a@b", ""), "First name only: no digits"},
		{"long", createForm("st1", "cut", "2026-01-14", strings.Repeat("a", 41), ""), "too long"},
		{"empty", createForm("st1", "cut", "2026-01-14", " ", ""), "Enter a first name"},
		{"inactive service", createForm("st1", "old", "2026-01-14", "Ana", ""), "not offered any more"},
		{"unknown staff", createForm("nope", "cut", "2026-01-14", "Ana", ""), "does not do this"},
		{"provider chair", createForm("st3", "cut", "2026-01-14", "Ana", ""), "does not do this"},
		{"past day", createForm("st1", "cut", "2026-01-13", "Ana", ""), "today or a later day"},
		{"bad date", createForm("st1", "cut", "tomorrow", "Ana", ""), "Check the details"},
	}
	for _, c := range cases {
		for _, act := range []string{"find", "create"} {
			t.Run(c.name+"/"+act, func(t *testing.T) {
				f := newFake(t)
				ans := run(t, newDeps(f), ask(act, now8(), cashier, c.form))
				has(t, ans.Document, c.want)
				if len(f.rows) != 0 || len(f.events) != 0 {
					t.Fatal("something was written")
				}
			})
		}
	}
}

func TestCreate_TimeTamperingAndTaken(t *testing.T) {
	f := newFake(t)
	f.add(t, "x", "st1", engine.Booked, jan14(t, 10, 0), 30, "Bo")
	d := newDeps(f)
	// overlap: refused with a visible reason naming the booking
	ans := run(t, d, ask("create", now8(), cashier, createForm("st1", "cut", "2026-01-14", "Ana", iso(jan14(t, 10, 15)))))
	has(t, ans.Document, "That time overlaps: 10:00-10:30 Bo.")
	// the cashier is not offered an override
	for _, n := range fieldNames(ans.Document) {
		if strings.HasPrefix(n, "override") {
			t.Fatal("override offered to a cashier")
		}
	}
	// off-grid time (10:07), outside hours (07:00), in the past (08:00 is now; 07:45), and garbage
	for _, tm := range []string{iso(jan14(t, 11, 7)), iso(jan14(t, 7, 0)), iso(jan14(t, 7, 45)), "soon", ""} {
		before := len(f.rows)
		ans := run(t, d, ask("create", now8(), cashier, createForm("st1", "cut", "2026-01-14", "Ana", tm)))
		if len(f.rows) != before {
			t.Fatalf("%q wrote a row", tm)
		}
		if !strings.Contains(text(ans.Document), "no longer free") && !strings.Contains(text(ans.Document), "not valid") {
			t.Fatalf("%q: %s", tm, text(ans.Document))
		}
	}
	if len(f.events) != 0 {
		t.Fatal("events written")
	}
}

func TestCreate_OverrideNeedsManagerPolicyReasonActor(t *testing.T) {
	setup := func(allow bool) (*fakeStore, Deps) {
		f := newFake(t)
		f.policy.AllowDoubleBooking = allow
		f.add(t, "x", "st1", engine.Booked, jan14(t, 10, 0), 30, "Bo")
		return f, newDeps(f)
	}
	over := func(reason string) map[string]any {
		m := createForm("st1", "cut", "2026-01-14", "Ana", "")
		m["override"], m["override_time"], m["override_reason"] = true, "10:15", reason
		return m
	}
	// manager + allowed + reason: written with an event naming the actor
	f, d := setup(true)
	run(t, d, ask("create", now8(), manager, over("regular, two chairs free later")))
	if len(f.rows) != 2 || len(f.events) != 2 {
		t.Fatalf("rows %d events %d", len(f.rows), len(f.events))
	}
	ov := f.events[0]
	if ov.Kind != engine.KindConflictOverride || ov.Reason != "regular, two chairs free later" || ov.ActorUserID != "u-mgr" ||
		len(ov.ConflictingIDs) != 1 || ov.ConflictingIDs[0] != "x" {
		t.Fatalf("%+v", ov)
	}
	// an overlap shows the override fields to a manager when the shop allows it
	f, d = setup(true)
	ans := run(t, d, ask("create", now8(), manager, createForm("st1", "cut", "2026-01-14", "Ana", iso(jan14(t, 10, 15)))))
	has(t, ans.Document, "Do not write client or health details")
	if got := strings.Join(fieldNames(ans.Document), ","); !strings.Contains(got, "override_reason") {
		t.Fatalf("%s", got)
	}
	// reason missing or blank
	for _, r := range []string{"", "   "} {
		f, d = setup(true)
		ans := run(t, d, ask("create", now8(), manager, over(r)))
		has(t, ans.Document, "Write a reason first")
		if len(f.rows) != 1 {
			t.Fatal("written without a reason")
		}
	}
	// reason too long (501 runes)
	f, d = setup(true)
	ans = run(t, d, ask("create", now8(), manager, over(strings.Repeat("é", 501))))
	has(t, ans.Document, "Check the details")
	if len(f.rows) != 1 {
		t.Fatal("written with a 501 rune reason")
	}
	// shop disallows double booking
	f, d = setup(false)
	ans = run(t, d, ask("create", now8(), manager, over("because")))
	has(t, ans.Document, "not allowed here")
	if len(f.rows) != 1 {
		t.Fatal("override with the setting off")
	}
	// cashier
	f, d = setup(true)
	ans = run(t, d, ask("create", now8(), cashier, over("because")))
	has(t, ans.Document, "not allowed here")
	if len(f.rows) != 1 {
		t.Fatal("cashier override")
	}
	// nobody signed in: no actor, no override
	f, d = setup(true)
	ans = run(t, d, ask("create", now8(), nil, over("because")))
	has(t, ans.Document, "Sign in to do this")
	if len(f.rows) != 1 {
		t.Fatal("override without actor")
	}
	// override on a free time writes plainly, no override event
	f, d = setup(true)
	m := over("fine")
	m["override_time"] = "15:00"
	run(t, d, ask("create", now8(), manager, m))
	if len(f.rows) != 2 || len(f.events) != 1 || f.events[0].Kind != engine.KindStateChange {
		t.Fatalf("%d %+v", len(f.rows), f.events)
	}
	// override time in the DST gap or garbage or the past
	for _, tm := range []string{"7:99", "xx", "07:00"} {
		f, d = setup(true)
		m := over("fine")
		m["override_time"] = tm
		run(t, d, ask("create", now8(), manager, m))
		if len(f.rows) != 1 {
			t.Fatalf("%q written", tm)
		}
	}
}

func TestCreate_OverrideTimeInSpringGapRefused(t *testing.T) {
	f := newFake(t)
	f.policy.AllowDoubleBooking = true
	d := newDeps(f)
	m := createForm("st1", "cut", "2026-03-29", "Ana", "")
	m["override"], m["override_time"], m["override_reason"] = true, "01:30", "night"
	ans := run(t, d, ask("create", time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC), manager, m))
	has(t, ans.Document, "not valid")
	if len(f.rows) != 0 {
		t.Fatal("gap time written")
	}
}

func TestCreate_StoreFailureIsAnError(t *testing.T) {
	f := newFake(t)
	f.failNext = errors.New("disk full")
	_, err := newDeps(f).Handle(ask("create", now8(), cashier, createForm("st1", "cut", "2026-01-14", "Ana", iso(jan14(t, 10, 0)))))
	if err == nil || err.Error() != "disk full" {
		t.Fatal(err)
	}
	if len(f.rows) != 0 {
		t.Fatal("partial write")
	}
}

func TestMove_SupersedesAndKeepsState(t *testing.T) {
	f := newFake(t)
	f.add(t, "old-1", "st1", engine.Confirmed, jan14(t, 10, 0), 30, "Ana")
	d := newDeps(f)
	start := run(t, d, ask("move_old_1", now8(), cashier, nil)).Document
	has(t, start, "Moving Ana (10:00-10:30)")
	for _, n := range fieldNames(start) {
		if n == "first_name" || n == "service" {
			t.Fatalf("move form asks %s", n)
		}
	}
	two := run(t, d, ask("findmove_old_1", now8(), cashier, map[string]any{"staff": "st2", "date": "2026-01-14"})).Document
	has(t, two, "Free times with Bo")
	// moving onto its own old time is fine: the old row does not block itself
	ans := run(t, d, ask("domove_old_1", now8(), cashier, map[string]any{"staff": "st1", "date": "2026-01-14", "time": iso(jan14(t, 10, 15))}))
	has(t, ans.Document, "Moved Ana to Ana at 10:15.")
	var nu Booking
	for id, b := range f.rows {
		if b.SupersedesID == "old-1" {
			nu = f.rows[id]
		}
	}
	if nu.ID == "" || nu.State != engine.Confirmed || nu.FirstName != "Ana" || !nu.Start.Equal(jan14(t, 10, 15)) || !f.superseded["old-1"] {
		t.Fatalf("%+v superseded=%v", nu, f.superseded)
	}
	// the old row is gone from the day
	if r := rowsOf(ans.Document); len(r) != 1 {
		t.Fatalf("%v", r)
	}
	// a second move from the stale button says it is gone
	ans = run(t, d, ask("move_old_1", now8(), cashier, nil))
	has(t, ans.Document, "no longer there")
}

func TestMove_ProviderBookingRefused(t *testing.T) {
	f := newFake(t)
	b := f.add(t, "pb", "st1", engine.Booked, jan14(t, 10, 0), 30, "Pia")
	b.Source = "provider"
	f.rows["pb"] = b
	d := newDeps(f)
	det := run(t, d, ask("open_pb", now8(), cashier, nil)).Document
	if hasAction(det, "move_pb") {
		t.Fatal("move offered for a provider booking")
	}
	has(t, det, "Booked at the provider")
	ans := run(t, d, ask("move_pb", now8(), cashier, nil))
	has(t, ans.Document, "Change it there")
	ans = run(t, d, ask("domove_pb", now8(), cashier, map[string]any{"staff": "st2", "date": "2026-01-14", "time": iso(jan14(t, 12, 0))}))
	has(t, ans.Document, "Change it there")
	if len(f.rows) != 1 {
		t.Fatal("provider booking moved")
	}
}

func TestMove_OverlapNamedAndNothingWritten(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	f.add(t, "b", "st2", engine.Booked, jan14(t, 12, 0), 30, "Bo")
	d := newDeps(f)
	ans := run(t, d, ask("domove_a", now8(), cashier, map[string]any{"staff": "st2", "date": "2026-01-14", "time": iso(jan14(t, 12, 15))}))
	has(t, ans.Document, "That time overlaps: 12:00-12:30 Bo.")
	if len(f.rows) != 2 || len(f.superseded) != 0 || len(f.events) != 0 {
		t.Fatal("written")
	}
}

func TestMove_DoesNotOfferCheckedIn(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.CheckedIn, jan14(t, 9, 0), 30, "Ana")
	det := run(t, newDeps(f), ask("open_a", jan14(t, 9, 5), cashier, nil)).Document
	if hasAction(det, "move_a") {
		t.Fatal("move on a checked-in booking")
	}
}

func TestTransitions_CheckInAndStart(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	d := newDeps(f)
	now := jan14(t, 9, 55)
	det := run(t, d, ask("open_a", now, cashier, nil)).Document
	if !hasAction(det, "do_checkin_a") || hasAction(det, "do_noshow_a") || hasAction(det, "ask_noshow_a") {
		t.Fatal(text(det))
	}
	has(t, det, "No-show can be set from 10:15")
	run(t, d, ask("do_checkin_a", now, cashier, nil))
	if f.rows["a"].State != engine.CheckedIn || len(f.events) != 1 || f.events[0].ToState != engine.CheckedIn {
		t.Fatalf("%+v %+v", f.rows["a"], f.events)
	}
	det = run(t, d, ask("open_a", now, cashier, nil)).Document
	if !hasAction(det, "do_start_a") || !hasAction(det, "do_unin_a") {
		t.Fatal(text(det))
	}
	run(t, d, ask("do_start_a", now, cashier, nil))
	if f.rows["a"].State != engine.InService {
		t.Fatal(f.rows["a"].State)
	}
	// stale button: checking in an in-service booking is refused with a reason
	ans := run(t, d, ask("do_checkin_a", now, cashier, nil))
	has(t, ans.Document, "cannot be done in the booking's current state")
	has(t, ans.Document, "cannot be completed from this page yet")
}

func TestTransitions_UndoCheckIn(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.CheckedIn, jan14(t, 10, 0), 30, "Ana")
	run(t, newDeps(f), ask("do_unin_a", jan14(t, 10, 1), cashier, nil))
	if f.rows["a"].State != engine.Booked {
		t.Fatal(f.rows["a"].State)
	}
}

func TestTransitions_NoShowGraceAndProviderConfirm(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	pb := f.add(t, "p", "st2", engine.Booked, jan14(t, 10, 0), 30, "Pia")
	pb.Source = "provider"
	f.rows["p"] = pb
	d := newDeps(f)
	// too early
	early := jan14(t, 10, 14)
	ans := run(t, d, ask("do_noshow_a", early, cashier, nil))
	has(t, ans.Document, "after the grace period")
	if f.rows["a"].State != engine.Booked {
		t.Fatal("early no-show written")
	}
	// at the boundary it is offered, through a confirm screen
	at := jan14(t, 10, 15)
	det := run(t, d, ask("open_a", at, cashier, nil)).Document
	if !hasAction(det, "ask_noshow_a") {
		t.Fatal(text(det))
	}
	conf := run(t, d, ask("ask_noshow_a", at, cashier, nil)).Document
	if !hasAction(conf, "do_noshow_a") {
		t.Fatal(text(conf))
	}
	run(t, d, ask("do_noshow_a", at, cashier, nil))
	if f.rows["a"].State != engine.NoShow {
		t.Fatal(f.rows["a"].State)
	}
	// provider: the confirm dialog warns and demands the tick
	pc := run(t, d, ask("ask_noshow_p", at, cashier, nil)).Document
	has(t, pc, "The provider may charge the client")
	if got := strings.Join(fieldNames(pc), ","); got != "ack" {
		t.Fatalf("%s", got)
	}
	// posting do_ without the tick shows the dialog again and writes nothing
	again := run(t, d, ask("do_noshow_p", at, cashier, map[string]any{"ack": false})).Document
	has(t, again, "The provider may charge the client")
	if f.rows["p"].State != engine.Booked {
		t.Fatal("provider no-show without the confirm")
	}
	run(t, d, ask("do_noshow_p", at, cashier, map[string]any{"ack": true}))
	if f.rows["p"].State != engine.NoShow {
		t.Fatal(f.rows["p"].State)
	}
}

func TestTransitions_UndoNoShow(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.NoShow, jan14(t, 10, 0), 30, "Ana")
	d := newDeps(f)
	now := jan14(t, 10, 40)
	// a cashier sees no button, only the hint, and a forged post is refused
	det := run(t, d, ask("open_a", now, cashier, nil)).Document
	if hasAction(det, "ask_unns_a") {
		t.Fatal("undo offered to a cashier")
	}
	has(t, det, "Ask a manager")
	ans := run(t, d, ask("do_unns_a", now, cashier, map[string]any{"reason": "x"}))
	has(t, ans.Document, "Only a manager")
	if f.rows["a"].State != engine.NoShow {
		t.Fatal("cashier undid a no-show")
	}
	// the manager needs a reason
	conf := run(t, d, ask("ask_unns_a", now, manager, nil)).Document
	has(t, conf, "Do not write client or health details")
	if got := strings.Join(fieldNames(conf), ","); got != "reason" {
		t.Fatal(got)
	}
	for _, r := range []string{"", "  "} {
		ans = run(t, d, ask("do_unns_a", now, manager, map[string]any{"reason": r}))
		has(t, ans.Document, "Write a reason first")
		if f.rows["a"].State != engine.NoShow || len(f.events) != 0 {
			t.Fatal("undone without a reason")
		}
	}
	// nobody signed in: no actor recorded, refused
	ans = run(t, d, ask("do_unns_a", now, nil, map[string]any{"reason": "arrived late"}))
	has(t, ans.Document, "Sign in to do this")
	run(t, d, ask("do_unns_a", now, manager, map[string]any{"reason": "arrived 10:16"}))
	if f.rows["a"].State != engine.CheckedIn || len(f.events) != 1 {
		t.Fatalf("%v %+v", f.rows["a"].State, f.events)
	}
	if e := f.events[0]; e.Reason != "arrived 10:16" || e.ActorUserID != "u-mgr" || e.FromState != engine.NoShow || e.ToState != engine.CheckedIn {
		t.Fatalf("%+v", e)
	}
}

func TestTransitions_UndoNoShowAfterWalkInTookTheChair(t *testing.T) {
	setup := func(allow bool) (*fakeStore, Deps) {
		f := newFake(t)
		f.policy.AllowDoubleBooking = allow
		f.add(t, "a", "st1", engine.NoShow, jan14(t, 10, 0), 30, "Ana")
		f.add(t, "w", "st1", engine.CheckedIn, jan14(t, 10, 20), 30, "Wes")
		return f, newDeps(f)
	}
	now := jan14(t, 10, 25)
	// N1: the correction is always allowed for a manager; the overlap is
	// written as a conflict_override event, whatever the double-booking setting.
	for _, allow := range []bool{false, true} {
		f, d := setup(allow)
		run(t, d, ask("do_unns_a", now, manager, map[string]any{"reason": "arrived"}))
		if f.rows["a"].State != engine.CheckedIn || len(f.events) != 2 || f.events[1].Kind != engine.KindConflictOverride ||
			f.events[1].Reason != "arrived" {
			t.Fatalf("allow=%v: %v %+v", allow, f.rows["a"].State, f.events)
		}
	}
}

func TestTransitions_CancelRules(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 14, 0), 30, "Ana")
	f.add(t, "b", "st1", engine.InService, jan14(t, 9, 0), 30, "Bo")
	d := newDeps(f)
	now := jan14(t, 9, 10)
	// client cancel 5 hours before: inside the 24 hour window -> late cancel flag
	conf := run(t, d, ask("ask_cancelc_a", now, cashier, nil)).Document
	if !hasAction(conf, "do_cancelc_a") {
		t.Fatal(text(conf))
	}
	run(t, d, ask("do_cancelc_a", now, cashier, nil))
	if f.rows["a"].State != engine.CancelledByClient || !f.rows["a"].LateCancel {
		t.Fatalf("%+v", f.rows["a"])
	}
	// the day view tags it
	d1 := run(t, d, ask("", now, cashier, nil)).Document
	has(t, d1, "late cancel")
	// in-service cancel by the shop needs a reason
	conf = run(t, d, ask("ask_cancels_b", now, cashier, nil)).Document
	if got := strings.Join(fieldNames(conf), ","); got != "reason" {
		t.Fatalf("%s", got)
	}
	ans := run(t, d, ask("do_cancels_b", now, cashier, map[string]any{"reason": " "}))
	has(t, ans.Document, "Write a reason first")
	if f.rows["b"].State != engine.InService {
		t.Fatal("cancelled without reason")
	}
	run(t, d, ask("do_cancels_b", now, cashier, map[string]any{"reason": "started on the wrong booking"}))
	if f.rows["b"].State != engine.CancelledByShop || len(f.events) != 2 || f.events[1].Reason != "started on the wrong booking" {
		t.Fatalf("%+v %+v", f.rows["b"], f.events)
	}
	// the reason of an ordinary cancel is not kept
	f.add(t, "c", "st2", engine.Booked, jan14(t, 12, 0), 30, "Cy")
	run(t, d, ask("do_cancels_c", now, cashier, map[string]any{"reason": "headache, doctor"}))
	if f.rows["c"].State != engine.CancelledByShop || f.events[2].Reason != "" {
		t.Fatalf("%+v", f.events[2])
	}
}

func TestTransitions_ProviderCancelNeedsAck(t *testing.T) {
	f := newFake(t)
	pb := f.add(t, "p", "st2", engine.Booked, jan14(t, 12, 0), 30, "Pia")
	pb.Source = "provider"
	f.rows["p"] = pb
	d := newDeps(f)
	run(t, d, ask("do_cancels_p", jan14(t, 9, 0), cashier, nil))
	if f.rows["p"].State != engine.Booked {
		t.Fatal("provider cancel without the confirm")
	}
	run(t, d, ask("do_cancels_p", jan14(t, 9, 0), cashier, map[string]any{"ack": true}))
	if f.rows["p"].State != engine.CancelledByShop {
		t.Fatal(f.rows["p"].State)
	}
}

func TestActions_UnknownAndTampered(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Completed, jan14(t, 9, 0), 30, "Ana")
	d := newDeps(f)
	for _, a := range []string{"bogus", "do_", "do_x_y", "do_checkin_nope", "open_", "day_notadate", "ask_unns", "seat_zzz", "drop_zzz", "ci_zzz", "domove_zzz", "findmove_zzz", "move_zzz"} {
		ans := run(t, d, ask(a, now8(), manager, nil))
		if ans.Document == nil {
			t.Fatalf("%s: no document", a)
		}
	}
	// a completed booking cannot be driven by a forged post
	ans := run(t, d, ask("do_cancels_a", now8(), manager, nil))
	has(t, ans.Document, "cannot be done")
	if f.rows["a"].State != engine.Completed {
		t.Fatal("completed booking changed")
	}
}

func TestActions_NavigationAndRedirect(t *testing.T) {
	f := newFake(t)
	d := newDeps(f)
	ans := run(t, d, ask("day_20260115", now8(), cashier, nil))
	has(t, ans.Document, "Thu 2026-01-15")
	ans = run(t, d, ask("today", now8(), cashier, nil))
	has(t, ans.Document, "Wed 2026-01-14")
	ans = run(t, d, Ask{View: ViewToday, Action: "opencal", Locale: "en", Now: now8()})
	if ans.Redirect != "/plugin/appointments" || ans.Document != nil {
		t.Fatalf("%+v", ans)
	}
}

func TestAnswer_MarshalNeedsExactlyOne(t *testing.T) {
	if _, err := (Answer{}).MarshalJSON(); err == nil {
		t.Fatal("empty answer marshalled")
	}
	if _, err := (Answer{Redirect: "/x", Document: &Document{Version: 1}}).MarshalJSON(); err == nil {
		t.Fatal("double answer marshalled")
	}
}

// Another event moved the booking between the decision and the write: the
// guarded write refuses (ErrGone) and the desk shows a notice, not an error
// page (fix-plug finding 1).
func TestTransition_RacedWriteIsANotice(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	f.failNext = ErrGone
	ans := run(t, newDeps(f), ask("do_checkin_a", jan14(t, 9, 55), cashier, nil))
	has(t, ans.Document, "no longer there")
}

// Another event booked the slot between the conflict check and the write: the
// store's guarded insert refuses with engine.ErrConflict and the cashier gets
// "no longer free", not an error page (fix-plug finding 2).
func TestCreate_RacedSlotIsANotice(t *testing.T) {
	f := newFake(t)
	f.failNext = engine.ErrConflict
	ans := run(t, newDeps(f), ask("create", now8(), cashier, createForm("st1", "cut", "2026-01-14", "Ana", iso(jan14(t, 10, 0)))))
	has(t, ans.Document, "no longer free")
}
