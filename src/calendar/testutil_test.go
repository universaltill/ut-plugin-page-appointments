package calendar

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

func london(t testing.TB) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// lt is a London wall-clock instant, in UTC.
func lt(t testing.TB, y int, m time.Month, d, h, mi int) time.Time {
	return time.Date(y, m, d, h, mi, 0, 0, london(t)).UTC()
}

func jan14(t testing.TB, h, mi int) time.Time { return lt(t, 2026, time.January, 14, h, mi) }

// fakeStore is the in-memory Store. It applies Change like a transaction and
// records every row and event so tests can dump them.
type fakeStore struct {
	shop        Shop
	staff       []Staff
	services    []ServiceInfo
	policy      engine.Policy
	rows        map[string]Booking
	superseded  map[string]bool
	events      []engine.Event
	queue       []Queued
	failNext    error
	boundsAsked [][2]time.Time
}

func newFake(t testing.TB) *fakeStore {
	t.Helper()
	hours := map[time.Weekday][]engine.Window{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		hours[d] = []engine.Window{{StartMin: 9 * 60, EndMin: 17 * 60}}
	}
	mk := func(id string) engine.Resource {
		return engine.Resource{ID: id, Kind: "staff", Active: true, WorkingHours: hours}
	}
	return &fakeStore{
		shop: Shop{TimeZone: "Europe/London", OriginTill: "till-main"},
		staff: []Staff{
			{Resource: mk("st1"), Name: "Ana"},
			{Resource: mk("st2"), Name: "Bo"},
			{Resource: mk("st3"), Name: "Provider Chair", ProviderMastered: true},
		},
		services: []ServiceInfo{
			{Service: engine.Service{SKU: "cut", DurationMin: 30, BufferMin: 5, All: true, Active: true}, Name: "Cut", Price: money.FromMinor(2500)},
			{Service: engine.Service{SKU: "colour", DurationMin: 60, All: true, Active: true}, Name: "Colour", Price: money.FromMinor(6000)},
			{Service: engine.Service{SKU: "old", DurationMin: 30, All: true, Active: false}, Name: "Retired", Price: money.FromMinor(1000)},
		},
		policy:     engine.DefaultPolicy(),
		rows:       map[string]Booking{},
		superseded: map[string]bool{},
	}
}

func (f *fakeStore) Shop() (Shop, error)              { return f.shop, nil }
func (f *fakeStore) Staff() ([]Staff, error)          { return f.staff, nil }
func (f *fakeStore) Services() ([]ServiceInfo, error) { return f.services, nil }
func (f *fakeStore) Policy() (engine.Policy, error)   { return f.policy, nil }
func (f *fakeStore) Queue() ([]Queued, error)         { return append([]Queued(nil), f.queue...), nil }

func (f *fakeStore) Bookings(from, to time.Time) ([]Booking, error) {
	f.boundsAsked = append(f.boundsAsked, [2]time.Time{from, to})
	var out []Booking
	for id, b := range f.rows {
		if f.superseded[id] {
			continue
		}
		end := b.End.Add(time.Duration(b.BufferMin) * time.Minute)
		if b.Start.Before(to) && from.Before(end) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeStore) Booking(id string) (Booking, bool, error) {
	b, ok := f.rows[id]
	if !ok || f.superseded[id] {
		return Booking{}, false, nil
	}
	return b, true, nil
}

func (f *fakeStore) Commit(c Change) error {
	if f.failNext != nil {
		e := f.failNext
		f.failNext = nil
		return e
	}
	if c.Insert != nil {
		f.rows[c.Insert.ID] = *c.Insert
	}
	if c.Superseded != "" {
		f.superseded[c.Superseded] = true
	}
	if c.Update != nil {
		f.rows[c.Update.ID] = *c.Update
	}
	f.events = append(f.events, c.Events...)
	if c.Dequeue != "" {
		var q []Queued
		for _, x := range f.queue {
			if x.ID != c.Dequeue {
				q = append(q, x)
			}
		}
		f.queue = q
	}
	if c.Enqueue != nil {
		f.queue = append(f.queue, *c.Enqueue)
	}
	return nil
}

func (f *fakeStore) add(t testing.TB, id, res string, st engine.State, start time.Time, dur int, name string) Booking {
	t.Helper()
	b := Booking{FirstName: name, Appointment: engine.Appointment{
		ID: id, ResourceID: res, State: st, Source: "own", Start: start,
		End: start.Add(time.Duration(dur) * time.Minute), BufferMin: 0,
		Lines: []engine.Line{{SKU: "cut", Name: "Cut", Qty: 1, Amount: 2500, DurationMin: dur}},
	}}
	f.rows[id] = b
	return b
}

func newDeps(f *fakeStore) Deps {
	n := 0
	return Deps{Store: f, Route: "/plugin/appointments", IDs: func() string { n++; return fmt.Sprintf("gen-%d", n) }}
}

var manager = &Operator{UserID: "u-mgr", Role: "manager"}
var cashier = &Operator{UserID: "u-cash", Role: "cashier"}

func ask(action string, now time.Time, op *Operator, form map[string]any) Ask {
	return Ask{View: ViewDay, Action: action, Form: form, Locale: "en", Operator: op, Now: now,
		Params: map[string]string{"date": "2026-01-14"}}
}

// run asks and fails the test on an error or an invalid document.
func run(t testing.TB, d Deps, a Ask) Answer {
	t.Helper()
	ans, err := d.Handle(a)
	if err != nil {
		t.Fatalf("%s: %v", a.Action, err)
	}
	if ans.Document != nil {
		if errs := CheckDocument(*ans.Document); len(errs) > 0 {
			t.Fatalf("%s: invalid document: %v", a.Action, errs)
		}
	}
	return ans
}

// text flattens every visible string of a document (keys resolved).
func text(d *Document) string {
	if d == nil {
		return ""
	}
	b, _ := json.Marshal(d)
	var sb strings.Builder
	sb.Write(b)
	for k, v := range bundles["en"] {
		if strings.Contains(string(b), `"key":"`+k+`"`) {
			sb.WriteString(" " + v)
		}
	}
	return sb.String()
}

func has(t testing.TB, d *Document, want string) {
	t.Helper()
	if !strings.Contains(text(d), want) {
		t.Fatalf("document lacks %q:\n%s", want, text(d))
	}
}

func lacks(t testing.TB, d *Document, bad string) {
	t.Helper()
	if strings.Contains(text(d), bad) {
		t.Fatalf("document has %q:\n%s", bad, text(d))
	}
}

func hasAction(d *Document, action string) bool {
	for _, c := range d.Components {
		if c["action"] == action {
			return true
		}
	}
	return false
}

func actionWithPrefix(t testing.TB, d *Document, prefix string) string {
	t.Helper()
	for _, c := range d.Components {
		if a, _ := c["action"].(string); strings.HasPrefix(a, prefix) {
			return a
		}
	}
	t.Fatalf("no action with prefix %q in %s", prefix, text(d))
	return ""
}

func fieldNames(d *Document) []string {
	var out []string
	for _, c := range d.Components {
		if c["type"] == "form" {
			for _, f := range c["fields"].([]Component) {
				out = append(out, f["name"].(string))
			}
		}
	}
	return out
}

func rowsOf(d *Document) [][]any {
	var out [][]any
	for _, c := range d.Components {
		if c["type"] == "table" {
			out = append(out, c["rows"].([][]any)...)
		}
	}
	return out
}

func is(err, target error) bool { return errors.Is(err, target) }
