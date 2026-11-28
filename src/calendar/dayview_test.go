package calendar

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

func TestDayView_TablePerStaffSortedWithLocalTimes(t *testing.T) {
	f := newFake(t)
	f.add(t, "b2", "st1", engine.Booked, jan14(t, 11, 0), 30, "Cy")
	f.add(t, "b1", "st1", engine.Confirmed, jan14(t, 9, 30), 30, "Ana")
	f.add(t, "b3", "st2", engine.CheckedIn, jan14(t, 10, 0), 60, "Dee")
	f.add(t, "b4", "st1", engine.CancelledByClient, jan14(t, 13, 0), 30, "Eve")
	ans := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0), Params: map[string]string{"date": "2026-01-14"}})
	d := ans.Document
	has(t, d, "Wed 2026-01-14")
	rows := rowsOf(d)
	if len(rows) != 4 {
		t.Fatalf("rows: %v", rows)
	}
	// st1 (Ana's chair) first: 09:30, 11:00, 13:00; then st2.
	want := []string{"09:30-10:00", "11:00-11:30", "13:00-13:30", "10:00-11:00"}
	for i, w := range want {
		if rows[i][0] != w {
			t.Errorf("row %d time %v want %s", i, rows[i][0], w)
		}
	}
	if rows[0][1] != "Ana" || rows[0][3] != "Confirmed" || rows[2][3] != "Cancelled by client" {
		t.Errorf("%v", rows)
	}
	// the provider-mastered chair has no booking button but a heading
	has(t, d, "Provider Chair")
	if !hasAction(d, "open_b1") || !hasAction(d, "open_b3") {
		t.Error("open buttons missing")
	}
	if hasAction(d, "open_b4") {
		t.Error("a cancelled booking has nothing to do at the desk")
	}
}

func TestDayView_SpringForwardDay(t *testing.T) {
	// 2026-03-29: clocks go forward 01:00 -> 02:00 in London, a 23 hour day.
	f := newFake(t)
	day := lt(t, 2026, time.March, 29, 0, 0)
	f.add(t, "late", "st1", engine.Booked, lt(t, 2026, time.March, 29, 23, 30), 30, "Ana") // inside
	f.add(t, "next", "st1", engine.Booked, lt(t, 2026, time.March, 30, 0, 30), 30, "Bo")   // next day
	f.add(t, "noon", "st1", engine.Booked, lt(t, 2026, time.March, 29, 12, 0), 30, "Cy")   // 11:00Z
	now := day.Add(-48 * time.Hour)
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: now, Params: map[string]string{"date": "2026-03-29"}}).Document
	rows := rowsOf(d)
	if len(rows) != 2 || rows[0][0] != "12:00-12:30" || rows[1][0] != "23:30-00:00" {
		t.Fatalf("%v", rows)
	}
	if got := f.boundsAsked[0]; got[1].Sub(got[0]) != 23*time.Hour {
		t.Fatalf("day span %v", got[1].Sub(got[0]))
	}
	n := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: now, Params: map[string]string{"date": "2026-03-30"}}).Document
	if r := rowsOf(n); len(r) != 1 || r[0][0] != "00:30-01:00" {
		t.Fatalf("%v", r)
	}
}

func TestDayView_AutumnBackDay(t *testing.T) {
	// 2026-10-25: clocks go back 02:00 -> 01:00, a 25 hour day.
	f := newFake(t)
	// 00:30Z is 01:30 BST; 01:30Z is 01:30 GMT: the same wall-clock label twice.
	f.add(t, "first", "st1", engine.Booked, time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), 30, "Ana")
	f.add(t, "second", "st1", engine.Booked, time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), 30, "Bo")
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: now, Params: map[string]string{"date": "2026-10-25"}}).Document
	if r := rowsOf(d); len(r) != 2 {
		t.Fatalf("%v", r)
	}
	if got := f.boundsAsked[0]; got[1].Sub(got[0]) != 25*time.Hour {
		t.Fatalf("day span %v", got[1].Sub(got[0]))
	}
	// a repeated wall-clock label is told apart by zone in the time select
	e := &env{loc: london(t)}
	opts, _ := e.slotOptions([]engine.Slot{
		{Start: time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)},
		{Start: time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)},
		{Start: time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC)},
	})
	got := []string{opts[0]["label"].(Component)["literal"].(string), opts[1]["label"].(Component)["literal"].(string), opts[2]["label"].(Component)["literal"].(string)}
	if got[0] != "01:30 BST" || got[1] != "01:30 GMT" || got[2] != "02:00" {
		t.Fatalf("%v", got)
	}
}

func TestDayView_DoubleBookingFromCacheIsShown(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 60, "Ana")
	pb := f.add(t, "p", "st1", engine.Booked, jan14(t, 10, 30), 30, "P")
	pb.Source = "provider"
	f.rows["p"] = pb
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0), Params: map[string]string{"date": "2026-01-14"}}).Document
	has(t, d, "2 bookings overlap")
	if r := rowsOf(d); !strings.Contains(r[0][3].(string), "overlap") || !strings.Contains(r[1][3].(string), "provider") {
		t.Fatalf("%v", r)
	}
}

func TestDayView_TimeOffAndHours(t *testing.T) {
	f := newFake(t)
	f.staff[1].TimeOff = []engine.TimeOff{{From: jan14(t, 0, 0), To: jan14(t, 23, 0), Reason: "sick, hospital"}}
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0), Params: map[string]string{"date": "2026-01-14"}}).Document
	has(t, d, "Working 09:00-17:00")
	has(t, d, "Time off: other") // the free text never reaches the screen
	lacks(t, d, "hospital")
}

func TestDayView_EmptyAndNoStaffStates(t *testing.T) {
	f := newFake(t)
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0)}).Document
	has(t, d, "No bookings")
	has(t, d, "Nobody is waiting")
	f.staff = nil
	d = run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0)}).Document
	has(t, d, "No staff set up")
}

func TestDayView_ManyBookingsStayInsideLimits(t *testing.T) {
	f := newFake(t)
	for i := 0; i < 90; i++ {
		id := "b" + strings.Repeat("x", 1) + string(rune('a'+i/26)) + string(rune('a'+i%26))
		f.add(t, id, "st1", engine.Booked, jan14(t, 9, 0).Add(time.Duration(i)*5*time.Minute), 5, "Ana")
	}
	d := run(t, newDeps(f), Ask{View: ViewDay, Locale: "en", Now: jan14(t, 8, 0), Params: map[string]string{"date": "2026-01-14"}}).Document
	has(t, d, "Only the first 60 bookings have buttons")
	if len(d.Components) > maxComponents {
		t.Fatal(len(d.Components))
	}
}

func TestDayView_NoNoteOrContactFields(t *testing.T) {
	f := newFake(t)
	f.add(t, "b1", "st1", engine.CheckedIn, jan14(t, 9, 0), 30, "Ana")
	f.queue = []Queued{{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{ID: "w1", Seq: 1, Arrived: jan14(t, 8, 0), DurationMin: 30}}}
	now := jan14(t, 9, 5)
	allowed := map[string]bool{"staff": true, "service": true, "date": true, "first_name": true, "time": true,
		"override": true, "override_time": true, "override_reason": true, "reason": true, "ack": true}
	f.policy.AllowDoubleBooking = true
	d := newDeps(f)
	docs := []*Document{
		run(t, d, ask("", now, manager, nil)).Document,
		run(t, d, ask("new", now, manager, nil)).Document,
		run(t, d, ask("find", now, manager, map[string]any{"staff": "st1", "service": "cut", "date": "2026-01-14", "first_name": "Zed"})).Document,
		run(t, d, ask("open_b1", now, manager, nil)).Document,
		run(t, d, ask("ask_cancels_b1", now, manager, nil)).Document,
		run(t, d, ask("move_b1", now, manager, nil)).Document,
	}
	f.rows["b1"] = func() Booking { b := f.rows["b1"]; b.State = engine.InService; return b }()
	docs = append(docs, run(t, d, ask("ask_cancels_b1", now, manager, nil)).Document)
	queueForm := false
	for _, doc := range docs {
		b, _ := json.Marshal(doc)
		for _, bad := range []string{"note", "phone", "email", "mobile", "allerg", "comment", "memo"} {
			if strings.Contains(strings.ToLower(string(b)), `"name":"`+bad) {
				t.Errorf("field %s present", bad)
			}
		}
		for _, n := range fieldNames(doc) {
			if n == "first_name" {
				queueForm = true
			}
			if !allowed[n] {
				t.Errorf("unexpected form field %q", n)
			}
		}
	}
	if !queueForm {
		t.Fatal("no first name field seen")
	}
}
