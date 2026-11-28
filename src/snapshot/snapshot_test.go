package snapshot_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/snapshot"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

var london = func() *time.Location {
	l, err := time.LoadLocation("Europe/London")
	if err != nil {
		panic(err)
	}
	return l
}()

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func booking(id, res string, start time.Time, mins int, st engine.State, name string, lines ...engine.Line) store.Booking {
	return store.Booking{
		Appointment: engine.Appointment{
			ID: id, OriginTillID: "till-main", Source: "own", ResourceID: res,
			Start: start, End: start.Add(time.Duration(mins) * time.Minute), BufferMin: 5,
			State: st, Lines: lines, Deposit: 0, SaleRef: "sale-secret", LateCancel: true,
		},
		FirstName: name, Currency: "GBP",
	}
}

func res(id, name string) store.ResourceRecord {
	return store.ResourceRecord{Resource: engine.Resource{ID: id, Kind: "staff", Active: true}, DisplayName: name, Colour: "#2a7", LogbookStaffRef: "logbook-secret"}
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return m
}

func keys(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	sort.Strings(k)
	return k
}

func base() snapshot.Input {
	now := utc(2026, time.November, 28, 9, 15)
	return snapshot.Input{
		Now: now, Zone: london,
		Resources: []store.ResourceRecord{res("r1", "Sam")},
		Bookings: []store.Booking{
			booking("a1", "r1", utc(2026, time.November, 28, 10, 30), 30, engine.Booked, "Ann", engine.Line{SKU: "FADE", Name: "Skin fade", Qty: 1, Amount: 2500}),
		},
	}
}

// The closed key set: nothing but the documented fields crosses (design 7207
// section 3). A new field in the store cannot leak by accident.
func TestBuild_ClosedKeySet_7207(t *testing.T) {
	raw, err := snapshot.Build(base())
	if err != nil {
		t.Fatal(err)
	}
	top := decode(t, raw)
	if got, want := keys(top), []string{"appointments", "as_of", "from_utc", "resources", "to_utc", "truncated", "v", "zone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	a := top["appointments"].([]any)[0].(map[string]any)
	if got, want := keys(a), []string{"b", "e", "id", "n", "r", "s", "st", "svc", "w"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("appointment keys = %v, want %v", got, want)
	}
	r := top["resources"].([]any)[0].(map[string]any)
	if got, want := keys(r), []string{"colour", "id", "kind", "name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resource keys = %v, want %v", got, want)
	}
	for _, leak := range []string{"sale-secret", "logbook-secret", "till-main", "2500", "late_cancel"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("payload leaks %q: %s", leak, raw)
		}
	}
	if a["n"] != "Ann" || a["st"] != "booked" || a["s"] != "2026-11-28T10:30:00Z" || a["e"] != "2026-11-28T11:00:00Z" || a["b"] != float64(5) || a["w"] != false {
		t.Errorf("appointment = %v", a)
	}
	if svc := a["svc"].([]any); len(svc) != 1 || svc[0] != "Skin fade" {
		t.Errorf("svc = %v", svc)
	}
	if top["v"] != float64(1) || top["zone"] != "Europe/London" || top["truncated"] != false || top["as_of"] != "2026-11-28T09:15:00Z" {
		t.Errorf("header = %v", top)
	}
}

func TestBuild_WindowAndRows_7207(t *testing.T) {
	in := base()
	in.Bookings = []store.Booking{
		booking("today", "r1", utc(2026, time.November, 28, 10, 0), 30, engine.Booked, "A"),
		booking("d2", "r1", utc(2026, time.November, 30, 10, 0), 30, engine.Confirmed, "B"),
		booking("d3", "r1", utc(2026, time.December, 1, 10, 0), 30, engine.Booked, "C"),       // day 4: outside
		booking("yday", "r1", utc(2026, time.November, 27, 10, 0), 30, engine.Completed, "D"), // yesterday: outside
		booking("cx1", "r1", utc(2026, time.November, 28, 11, 0), 30, engine.CancelledByClient, "E"),
		booking("cx2", "r1", utc(2026, time.November, 28, 12, 0), 30, engine.CancelledByShop, "F"),
		booking("done", "r1", utc(2026, time.November, 28, 8, 0), 30, engine.Completed, "G"),
		booking("ns", "r1", utc(2026, time.November, 28, 9, 0), 30, engine.NoShow, "H"),
	}
	in.Bookings[0].WalkIn = true
	provider := booking("prov", "r1", utc(2026, time.November, 28, 13, 0), 30, engine.Booked, "Zed")
	provider.Source = "provider"
	in.Bookings = append(in.Bookings, provider)
	raw, err := snapshot.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range decode(t, raw)["appointments"].([]any) {
		ids = append(ids, a.(map[string]any)["id"].(string))
	}
	// sorted by start then id: done 08:00, ns 09:00, today 10:00, d2 (30 Nov 10:00)
	if want := []string{"done", "ns", "today", "d2"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v (cancelled, provider and out-of-window rows are not sent)", ids, want)
	}
	if !strings.Contains(string(raw), `"w":true`) {
		t.Error("walk-in flag lost")
	}
}

// Window maths are in the shop zone: on the day the clocks go back (25 hours)
// the window still ends at the third local midnight.
func TestWindow_DST_7207(t *testing.T) {
	from, to := snapshot.Window(utc(2026, time.October, 24, 12, 0), london) // BST; clocks go back on 25 Oct
	if !from.Equal(utc(2026, time.October, 23, 23, 0)) {
		t.Errorf("from = %v, want 23:00Z the evening before (BST midnight)", from)
	}
	if !to.Equal(utc(2026, time.October, 27, 0, 0)) {
		t.Errorf("to = %v, want 27 Oct 00:00Z (GMT midnight, 25 h day crossed)", to)
	}
	from, to = snapshot.Window(utc(2026, time.November, 28, 9, 15), london)
	if !from.Equal(utc(2026, time.November, 28, 0, 0)) || !to.Equal(utc(2026, time.December, 1, 0, 0)) {
		t.Errorf("winter window = %v..%v", from, to)
	}
}

func TestBuild_TextBounds_7207(t *testing.T) {
	in := base()
	long := strings.Repeat("é", 60)
	in.Bookings[0].FirstName = long
	in.Bookings[0].Lines = nil
	for i := 0; i < 5; i++ {
		in.Bookings[0].Lines = append(in.Bookings[0].Lines, engine.Line{SKU: fmt.Sprint(i), Name: strings.Repeat("s", 60) + fmt.Sprint(i), Qty: 1})
	}
	raw, err := snapshot.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	a := decode(t, raw)["appointments"].([]any)[0].(map[string]any)
	if n := []rune(a["n"].(string)); len(n) != 40 {
		t.Errorf("first name = %d runes, want cut at 40", len(n))
	}
	svc := a["svc"].([]any)
	if len(svc) != 3 {
		t.Fatalf("svc = %d, want 3", len(svc))
	}
	for _, s := range svc {
		if n := len([]rune(s.(string))); n != 40 {
			t.Errorf("service name = %d runes, want 40", n)
		}
	}
}

func TestBuild_ResourcesActiveAndBounded_7207(t *testing.T) {
	in := base()
	in.Resources = nil
	for i := 0; i < 30; i++ {
		in.Resources = append(in.Resources, res(fmt.Sprintf("r%02d", i), fmt.Sprintf("Staff %d", i)))
	}
	in.Resources[0].Active = false
	raw, err := snapshot.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	rs := decode(t, raw)["resources"].([]any)
	if len(rs) != 20 {
		t.Fatalf("resources = %d, want 20 (cap)", len(rs))
	}
	if rs[0].(map[string]any)["id"] != "r01" {
		t.Errorf("first resource = %v, want r01 (r00 is inactive)", rs[0])
	}
}

// 1,000 appointments do not fit: the earliest rows stay, the flag is set, the
// payload is within the cap (design 7207 section 3).
func TestBuild_TruncatesAtCap_7207(t *testing.T) {
	in := base()
	in.Bookings = nil
	for i := 0; i < 1000; i++ {
		start := utc(2026, time.November, 28, 0, 0).Add(time.Duration(i) * time.Minute)
		in.Bookings = append(in.Bookings, booking(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "r1", start, 1, engine.Booked,
			strings.Repeat("é", 40), engine.Line{Name: strings.Repeat("s", 40), Qty: 1}))
	}
	raw, err := snapshot.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > snapshot.MaxBytes {
		t.Fatalf("payload = %d bytes, cap %d", len(raw), snapshot.MaxBytes)
	}
	m := decode(t, raw)
	if m["truncated"] != true {
		t.Error("truncated flag not set")
	}
	as := m["appointments"].([]any)
	if len(as) == 0 || len(as) >= 1000 {
		t.Fatalf("kept %d rows", len(as))
	}
	if as[0].(map[string]any)["id"] != "00000000-0000-4000-8000-000000000000" {
		t.Errorf("first kept = %v, want the earliest row", as[0])
	}
	// a small day is never flagged
	raw, _ = snapshot.Build(base())
	if decode(t, raw)["truncated"] != false {
		t.Error("small payload flagged truncated")
	}
}

// Equal input gives equal bytes, so snapshot_set does not move the version.
func TestBuild_Deterministic_7207(t *testing.T) {
	a, _ := snapshot.Build(base())
	in := base()
	in.Bookings = append([]store.Booking{}, in.Bookings...)
	b, _ := snapshot.Build(in)
	if string(a) != string(b) {
		t.Fatal("same input, different bytes")
	}
}

func TestBuild_InvalidInput_7207(t *testing.T) {
	in := base()
	in.Zone = nil
	if _, err := snapshot.Build(in); err == nil {
		t.Error("nil zone accepted")
	}
	in = base()
	in.Now = time.Time{}
	if _, err := snapshot.Build(in); err == nil {
		t.Error("zero now accepted")
	}
}

// Changed decides whether the 15-minute tick writes (design section 4): the
// content moved, the window moved, or as_of is 60 minutes old.
func TestChanged_7207(t *testing.T) {
	now := utc(2026, time.November, 28, 9, 15)
	prev, _ := snapshot.Build(base())
	same := base()
	same.Now = now.Add(15 * time.Minute) // only as_of differs
	next, _ := snapshot.Build(same)
	if snapshot.Changed(prev, next, same.Now) {
		t.Error("only as_of moved, 15 min old: must not write")
	}
	old := base()
	old.Now = now.Add(61 * time.Minute)
	next, _ = snapshot.Build(old)
	if !snapshot.Changed(prev, next, old.Now) {
		t.Error("as_of 61 minutes old: must write")
	}
	moved := base()
	moved.Now = now.Add(15 * time.Minute)
	moved.Bookings = append(moved.Bookings, booking("a2", "r1", utc(2026, time.November, 28, 15, 0), 30, engine.Booked, "Bo"))
	next, _ = snapshot.Build(moved)
	if !snapshot.Changed(prev, next, moved.Now) {
		t.Error("content changed: must write")
	}
	if !snapshot.Changed(nil, prev, now) {
		t.Error("no previous snapshot: must write")
	}
	midnight := base()
	midnight.Now = utc(2026, time.November, 29, 0, 5)
	next, _ = snapshot.Build(midnight)
	if !snapshot.Changed(prev, next, midnight.Now) {
		t.Error("window moved at local midnight: must write")
	}
	if !snapshot.Changed([]byte("not json"), prev, now) {
		t.Error("corrupt previous snapshot: must write")
	}
}
