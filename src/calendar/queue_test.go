package calendar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

func TestQueue_AddShowsEstimateInArrivalOrder(t *testing.T) {
	f := newFake(t)
	f.staff[2].Active = false
	// both chairs busy: Ana's chair until 10:35 (30 + 5 buffer), Bo's until 11:00
	b := f.add(t, "a", "st1", engine.InService, jan14(t, 10, 0), 30, "Ana")
	b.BufferMin = 5
	f.rows["a"] = b
	f.add(t, "b", "st2", engine.InService, jan14(t, 10, 0), 60, "Bo")
	d := newDeps(f)
	now := jan14(t, 10, 10)
	ans := run(t, d, ask("queue_add", now, cashier, map[string]any{"first_name": " Sam ", "service": "cut", "staff": "any"}))
	has(t, ans.Document, "Sam added to the queue.")
	now2 := now.Add(time.Minute)
	ans = run(t, d, ask("queue_add", now2, cashier, map[string]any{"first_name": "", "service": "cut", "staff": "st2"}))
	if len(f.queue) != 2 || f.queue[0].FirstName != "Sam" || f.queue[1].Seq != 2 || f.queue[1].ResourceIDs[0] != "st2" {
		t.Fatalf("%+v", f.queue)
	}
	rows := rowsOf(ans.Document)
	// the day table of 10:00 bookings comes first, then the queue (3 columns kinds differ)
	var q [][]any
	for _, r := range rows {
		if len(r) == 4 {
			if _, ok := r[0].(float64); ok {
				q = append(q, r)
			}
		}
	}
	if len(q) != 2 || q[0][1] != "Sam" || q[1][1] != "Walk-in" {
		t.Fatalf("%v", rows)
	}
	// Sam waits for Ana's chair at 10:35; the second walk-in is pinned to Bo's chair, free at 11:00
	if q[0][3] != "10:35 Ana" || q[1][3] != "11:00 Bo" {
		t.Fatalf("%v", q)
	}
	if !hasAction(ans.Document, "seat_w_gen_1") && actionWithPrefix(t, ans.Document, "seat_") == "" {
		t.Fatal("no seat button")
	}
}

func TestQueue_AddNameRules(t *testing.T) {
	f := newFake(t)
	d := newDeps(f)
	for _, n := range []string{"Sam 07700", "x@y", strings.Repeat("a", 41)} {
		ans := run(t, d, ask("queue_add", now8(), cashier, map[string]any{"first_name": n, "service": "cut", "staff": "any"}))
		has(t, ans.Document, "First name only")
		_ = ans
	}
	if len(f.queue) != 0 {
		t.Fatal("bad name queued")
	}
	run(t, d, ask("queue_add", now8(), cashier, map[string]any{"first_name": "Sam", "service": "old", "staff": "any"}))
	run(t, d, ask("queue_add", now8(), cashier, map[string]any{"first_name": "Sam", "service": "cut", "staff": "ghost"}))
	if len(f.queue) != 0 {
		t.Fatal("bad service or staff queued")
	}
}

func TestQueue_SeatWhenChairFree(t *testing.T) {
	f := newFake(t)
	f.staff[2].Active = false
	d := newDeps(f)
	now := jan14(t, 10, 0)
	run(t, d, ask("queue_add", now, cashier, map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"}))
	seat := actionWithPrefix(t, run(t, d, ask("", now, cashier, nil)).Document, "seat_")
	ans := run(t, d, ask(seat, now, cashier, nil))
	has(t, ans.Document, "Sam is seated with Ana.")
	if len(f.queue) != 0 || len(f.rows) != 1 {
		t.Fatalf("queue %d rows %d", len(f.queue), len(f.rows))
	}
	var w Booking
	for _, w = range f.rows {
	}
	if !w.WalkIn || w.State != engine.CheckedIn || w.Source != "own" || w.ResourceID != "st1" || !w.Start.Equal(now) || w.FirstName != "Sam" {
		t.Fatalf("%+v", w)
	}
	if len(f.events) != 1 || f.events[0].FromState != "" || f.events[0].ToState != engine.CheckedIn {
		t.Fatalf("%+v", f.events)
	}
	// a second press (stale button) is a notice
	ans = run(t, d, ask(seat, now, cashier, nil))
	has(t, ans.Document, "no longer there")
}

func TestQueue_SeatRefusedWhileEveryChairBusy(t *testing.T) {
	f := newFake(t)
	f.staff[2].Active = false
	f.add(t, "a", "st1", engine.InService, jan14(t, 10, 0), 60, "Ana")
	f.add(t, "b", "st2", engine.InService, jan14(t, 10, 0), 60, "Bo")
	d := newDeps(f)
	now := jan14(t, 10, 5)
	run(t, d, ask("queue_add", now, cashier, map[string]any{"first_name": "Sam", "service": "cut", "staff": "any"}))
	seat := actionWithPrefix(t, run(t, d, ask("", now, cashier, nil)).Document, "seat_")
	ans := run(t, d, ask(seat, now, cashier, nil))
	has(t, ans.Document, "No chair is free yet. The earliest is 11:00 with Ana.")
	if len(f.queue) != 1 || len(f.rows) != 2 {
		t.Fatal("seated onto a busy chair")
	}
}

func TestQueue_NoChairAtAll(t *testing.T) {
	f := newFake(t)
	f.staff = []Staff{}
	f.staff = append(f.staff, Staff{Resource: engine.Resource{ID: "x", Active: false}, Name: "Gone"})
	f.queue = []Queued{{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{ID: "w1", Seq: 1, Arrived: jan14(t, 9, 0), DurationMin: 30}}}
	d := newDeps(f)
	ans := run(t, d, ask("", jan14(t, 9, 0), cashier, nil))
	has(t, ans.Document, "No chair free in the next days")
	ans = run(t, d, ask("seat_w1", jan14(t, 9, 0), cashier, nil))
	has(t, ans.Document, "No chair free in the next days")
}

func TestQueue_Drop(t *testing.T) {
	f := newFake(t)
	f.queue = []Queued{{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{ID: "w1", Seq: 1, Arrived: jan14(t, 9, 0), DurationMin: 30}}}
	ans := run(t, newDeps(f), ask("drop_w1", jan14(t, 9, 0), cashier, nil))
	has(t, ans.Document, "Sam removed from the queue.")
	if len(f.queue) != 0 {
		t.Fatal("not removed")
	}
	has(t, ans.Document, "Nobody is waiting")
}

func TestQueue_ProviderMasteredChairTakesWalkIns(t *testing.T) {
	f := newFake(t)
	f.staff = []Staff{f.staff[2]} // only the provider-mastered chair
	f.queue = []Queued{{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{ID: "w1", Seq: 1, Arrived: jan14(t, 9, 0), DurationMin: 30}}}
	d := newDeps(f)
	now := jan14(t, 10, 0)
	ans := run(t, d, ask("seat_w1", now, cashier, nil))
	has(t, ans.Document, "Sam is seated with Provider Chair.")
}

func TestOverlay_TodayAndQuickCheckIn(t *testing.T) {
	f := newFake(t)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 10, 0), 30, "Ana")
	f.add(t, "b", "st2", engine.CheckedIn, jan14(t, 9, 0), 30, "Bo")
	f.add(t, "c", "st1", engine.CancelledByShop, jan14(t, 11, 0), 30, "Cy")
	f.add(t, "n", "st2", engine.NoShow, jan14(t, 8, 0), 30, "Nel")
	f.queue = []Queued{{FirstName: "Sam", ServiceSKU: "cut", Walkin: engine.Walkin{ID: "w1", Seq: 1, DurationMin: 30}}}
	d := newDeps(f)
	now := jan14(t, 9, 50)
	ov := Ask{View: ViewToday, Locale: "en", Now: now, Operator: cashier}
	doc := run(t, d, ov).Document
	rows := rowsOf(doc)
	if len(rows) != 2 || rows[0][1] != "Bo" || rows[1][1] != "Ana" {
		t.Fatalf("%v", rows)
	}
	has(t, doc, "1 walk-in(s) waiting")
	if !hasAction(doc, "ci_a") || hasAction(doc, "ci_b") || !hasAction(doc, "opencal") {
		t.Fatal(text(doc))
	}
	ov.Action = "ci_a"
	ans := run(t, d, ov)
	has(t, ans.Document, "Check in done for Ana.")
	if f.rows["a"].State != engine.CheckedIn || ans.Document.Title["key"] != "appt.cal.today_title" {
		t.Fatalf("%v", f.rows["a"].State)
	}
	// stale tap: refused inside the overlay
	ans = run(t, d, ov)
	has(t, ans.Document, "cannot be done")
	ov.Action = "ci_ghost"
	has(t, run(t, d, ov).Document, "no longer there")
	// empty day
	g := newFake(t)
	has(t, run(t, newDeps(g), Ask{View: ViewToday, Locale: "en", Now: now}).Document, "Nothing booked today")
}

func TestOverlay_ManyBookingsCapped(t *testing.T) {
	f := newFake(t)
	for i := 0; i < 12; i++ {
		f.add(t, "b"+string(rune('a'+i)), "st1", engine.Booked, jan14(t, 9, 0).Add(time.Duration(i)*20*time.Minute), 15, "Ana")
	}
	doc := run(t, newDeps(f), Ask{View: ViewToday, Locale: "en", Now: jan14(t, 8, 0)}).Document
	if len(rowsOf(doc)) != 8 {
		t.Fatal(len(rowsOf(doc)))
	}
	has(t, doc, "4 more today")
}

// ---- i18n and document hygiene ----

func enKeys(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile("../../locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestI18n_EverySourceKeyExistsAndNoneIsUnused(t *testing.T) {
	en := enKeys(t)
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`"(appt\.cal\.[a-z0-9_.]*)"`)
	used := map[string]bool{}
	var prefixes []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			k := m[1]
			if strings.HasSuffix(k, ".") {
				prefixes = append(prefixes, k) // dynamic: state, err, timeoff, wd
				continue
			}
			used[k] = true
			if _, ok := en[k]; !ok {
				t.Errorf("%s: key %q missing from locales/en.json", f, k)
			}
		}
	}
	for k := range en {
		if used[k] {
			continue
		}
		dyn := false
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				dyn = true
			}
		}
		if strings.HasPrefix(k, "appt.cal.wd.") || strings.HasPrefix(k, "appt.cal.timeoff.") {
			dyn = true // built with fmt in env.go / dayview.go
		}
		if !dyn {
			t.Errorf("key %q is unused", k)
		}
	}
}

func TestI18n_EveryErrorAndStateHasText(t *testing.T) {
	en := enKeys(t)
	for _, err := range userErrors {
		if en["appt.cal.err."+err.Error()] == "" {
			t.Errorf("error %q has no text", err)
		}
	}
	for _, s := range []engine.State{engine.Booked, engine.Confirmed, engine.CheckedIn, engine.InService, engine.Completed,
		engine.CancelledByClient, engine.CancelledByShop, engine.NoShow} {
		if en["appt.cal.state."+string(s)] == "" {
			t.Errorf("state %s has no text", s)
		}
	}
	for d := 0; d < 7; d++ {
		if en["appt.cal.wd."+string(rune('0'+d))] == "" {
			t.Errorf("weekday %d", d)
		}
	}
	for _, v := range verbs {
		if en[v.label] == "" {
			t.Errorf("verb %s", v.name)
		}
	}
	// placeholders match the arguments the code passes (a few risky ones)
	for k, want := range map[string]int{"appt.cal.booked": 3, "appt.cal.moved": 3, "appt.cal.confirm_q": 3, "appt.cal.d_time": 2, "appt.cal.times_for": 3} {
		if n := strings.Count(en[k], "%"); n != want {
			t.Errorf("%s has %d verbs, want %d", k, n, want)
		}
	}
}

func TestI18n_UnknownLocaleFallsBackAndMissingKeyIsVisible(t *testing.T) {
	if got := (tr{"fr"}).s("appt.cal.today"); got != "Today" {
		t.Fatal(got)
	}
	if got := (tr{"en"}).s("appt.cal.nope"); got != "appt.cal.nope" {
		t.Fatal(got)
	}
	f := newFake(t)
	doc := run(t, newDeps(f), Ask{View: ViewDay, Locale: "fa-IR", Now: jan14(t, 8, 0)}).Document
	has(t, doc, "Wed 2026-01-14")
}

func TestI18n_LongLocaleTextStaysInLimits(t *testing.T) {
	// Text expands; nothing here is sized in characters except the 4 KiB string
	// limit, which CheckDocument asserts for every document the tests build.
	f := newFake(t)
	f.staff[0].Name = strings.Repeat("Ä", 40)
	f.add(t, "a", "st1", engine.Booked, jan14(t, 9, 0), 30, strings.Repeat("Ö", 40))
	run(t, newDeps(f), ask("open_a", jan14(t, 8, 0), cashier, nil))
}

func TestCheckDocument_CatchesBadDocuments(t *testing.T) {
	bad := Document{Version: 2, Components: []Component{
		{"type": "button", "action": "Bad Name", "label": Key("k"), "style": "big"},
		{"type": "nope"},
		{"type": "table", "columns": []Component{{}}, "rows": [][]any{{1, 2}}},
		{"type": "form", "action": "a", "submit": Key("s"), "fields": []Component{
			{"name": "x", "label": Key("l"), "kind": "text"}, {"name": "x", "label": Key("l"), "kind": "weird"},
			{"name": "s", "label": Key("l"), "kind": "select"}}},
		{"type": "notice", "level": "loud", "text": Component{"key": "a", "literal": "b"}},
	}}
	if errs := CheckDocument(bad); len(errs) < 9 {
		t.Fatalf("%d problems: %v", len(errs), errs)
	}
	var many Document
	many.Version = 1
	for i := 0; i < 201; i++ {
		many.Components = append(many.Components, heading(Lit("x")))
	}
	if errs := CheckDocument(many); len(errs) != 1 {
		t.Fatal(errs)
	}
}

// A dump of what the calendar can hand to the store holds no phone, email or
// note: the types have no such field (appointment-v1 sections 1 and 7).
func TestNoContactOrNoteFieldsInStoredTypes(t *testing.T) {
	var names []string
	var walk func(rt reflect.Type)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt.PkgPath() == "time" {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			names = append(names, strings.ToLower(rt.Field(i).Name))
			walk(rt.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(Change{}))
	for _, n := range names {
		for _, bad := range []string{"phone", "email", "mobile", "note", "comment", "surname", "lastname", "address"} {
			if strings.Contains(n, bad) {
				t.Errorf("stored type has field %q", n)
			}
		}
	}
	if len(names) < 20 {
		t.Fatalf("walked only %d fields", len(names))
	}
}
