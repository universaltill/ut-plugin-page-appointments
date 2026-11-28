package engine

import (
	"fmt"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

var _ = money.Zero

type fakeClock struct{ t time.Time }

func (c fakeClock) Now() time.Time { return c.t }

func counterIDs(prefix string) IDGen {
	n := 0
	return func() string { n++; return fmt.Sprintf("%s%d", prefix, n) }
}

func london(t testing.TB) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// lt builds a UTC instant from a London wall-clock time.
func lt(t testing.TB, y int, m time.Month, d, h, mi int) time.Time {
	t.Helper()
	return time.Date(y, m, d, h, mi, 0, 0, london(t)).UTC()
}

// jan14 is Wednesday 2026-01-14 in London (GMT, so local == UTC).
func jan14(t testing.TB, h, mi int) time.Time { return lt(t, 2026, time.January, 14, h, mi) }

func hours(startMin, endMin int) map[time.Weekday][]Window {
	m := map[time.Weekday][]Window{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		m[d] = []Window{{StartMin: startMin, EndMin: endMin}}
	}
	return m
}

func res(id string, startMin, endMin int) Resource {
	return Resource{ID: id, Kind: "chair", Active: true, WorkingHours: hours(startMin, endMin)}
}

func svc(dur, buf int) (Service, Line) {
	return Service{SKU: "s", DurationMin: dur, BufferMin: buf, All: true, Active: true},
		Line{SKU: "s", Name: "S", Qty: 1, Amount: 1000, DurationMin: dur, BufferMin: buf}
}

func req(dur, buf int) Request {
	s, l := svc(dur, buf)
	return Request{Services: []Service{s}, Lines: []Line{l}}
}

func appt(id, resID string, st State, start time.Time, durMin, bufMin int) Appointment {
	return Appointment{ID: id, ResourceID: resID, State: st, Source: "own", Start: start,
		End: start.Add(time.Duration(durMin) * time.Minute), BufferMin: bufMin}
}

func hm(ts []Slot, loc *time.Location) []string {
	out := make([]string, len(ts))
	for i, s := range ts {
		out[i] = s.Start.In(loc).Format("15:04")
	}
	return out
}
