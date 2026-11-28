package engine

import (
	"reflect"
	"testing"
	"time"
)

func TestWalkIn_Order(t *testing.T) {
	q := []Walkin{
		{ID: "W3", Seq: 3, Arrived: jan14(t, 10, 5)},
		{ID: "W1", Seq: 2, Arrived: jan14(t, 10, 0)},
		{ID: "W2", Seq: 1, Arrived: jan14(t, 10, 0)},
		{ID: "b", Seq: 3, Arrived: jan14(t, 10, 5)},
		{ID: "a", Seq: 3, Arrived: jan14(t, 10, 5)},
	}
	before := append([]Walkin(nil), q...)
	got := OrderQueue(q)
	ids := []string{}
	for _, w := range got {
		ids = append(ids, w.ID)
	}
	if !reflect.DeepEqual(ids, []string{"W2", "W1", "W3", "a", "b"}) && !reflect.DeepEqual(ids, []string{"W2", "W1", "W3", "a", "b"}) {
		t.Fatalf("order %v", ids)
	}
	if !reflect.DeepEqual(q, before) || !reflect.DeepEqual(OrderQueue(q), got) {
		t.Fatal("input mutated or non-deterministic")
	}
}

func estFixture(t *testing.T) ([]Resource, []Appointment) {
	r1, r2 := res("r1", 540, 1080), res("r2", 540, 1080)
	ex := []Appointment{
		appt("i1", "r1", InService, jan14(t, 10, 0), 40, 5),
		appt("b1", "r2", Booked, jan14(t, 10, 30), 30, 0),
	}
	ex[0].End = jan14(t, 10, 40)
	return []Resource{r1, r2}, ex
}

func est(t *testing.T, rs []Resource, ex []Appointment, q []Walkin, next Walkin, now time.Time) Estimate {
	t.Helper()
	return EstimateNearestFree(london(t), rs, ex, q, next, DefaultPolicy(), now)
}

func TestWalkIn_EstimateBase(t *testing.T) {
	rs, ex := estFixture(t)
	e := est(t, rs, ex, nil, Walkin{ID: "n", DurationMin: 30}, jan14(t, 10, 7))
	if !e.OK || e.ResourceID != "r1" || e.Start != jan14(t, 10, 45) {
		t.Fatalf("%+v", e)
	}
}

func TestWalkIn_QueueAhead(t *testing.T) {
	rs, ex := estFixture(t)
	q := []Walkin{{ID: "q", Seq: 1, Arrived: jan14(t, 10, 0), DurationMin: 30}}
	e := est(t, rs, ex, q, Walkin{ID: "n", DurationMin: 30}, jan14(t, 10, 7))
	if !e.OK || e.ResourceID != "r2" || e.Start != jan14(t, 11, 0) {
		t.Fatalf("%+v", e)
	}
	// queue order matters: an unplaceable queue item is skipped
	q = []Walkin{{ID: "q", DurationMin: 0}}
	e = est(t, rs, ex, q, Walkin{ID: "n", DurationMin: 30}, jan14(t, 10, 7))
	if e.ResourceID != "r1" {
		t.Fatalf("%+v", e)
	}
}

func TestWalkIn_FitsBefore(t *testing.T) {
	rs, ex := estFixture(t)
	e := est(t, rs, ex, nil, Walkin{ID: "n", DurationMin: 20}, jan14(t, 10, 7))
	if !e.OK || e.ResourceID != "r2" || e.Start != jan14(t, 10, 7) {
		t.Fatalf("%+v", e)
	}
	// buffer pushes it out of the gap
	e = est(t, rs, ex, nil, Walkin{ID: "n", DurationMin: 20, BufferMin: 3}, jan14(t, 10, 7))
	if e.ResourceID != "r2" || e.Start != jan14(t, 10, 7) {
		t.Fatalf("20+3 fits: %+v", e)
	}
	e = est(t, rs, ex, nil, Walkin{ID: "n", DurationMin: 20, BufferMin: 10}, jan14(t, 10, 7))
	if e.ResourceID != "r1" || e.Start != jan14(t, 10, 45) {
		t.Fatalf("20+10: %+v", e)
	}
}

func TestWalkIn_RoundUp(t *testing.T) {
	rs := []Resource{res("r1", 540, 1080)}
	e := est(t, rs, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 7).Add(30*time.Second))
	if e.Start != jan14(t, 10, 8) {
		t.Fatalf("%+v", e)
	}
	e = est(t, rs, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 7))
	if e.Start != jan14(t, 10, 7) {
		t.Fatalf("exact minute kept: %+v", e)
	}
}

func TestWalkIn_Tie(t *testing.T) {
	rs := []Resource{res("r2", 540, 1080), res("r1", 540, 1080)}
	e := est(t, rs, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 0))
	if e.ResourceID != "r1" {
		t.Fatalf("%+v", e)
	}
	e = est(t, rs, nil, nil, Walkin{DurationMin: 30, ResourceIDs: []string{"r2"}}, jan14(t, 10, 0))
	if e.ResourceID != "r2" {
		t.Fatalf("%+v", e)
	}
	q := []Walkin{{ID: "q", Seq: 1, DurationMin: 30, ResourceIDs: []string{"r2"}}}
	e = est(t, rs, nil, q, Walkin{DurationMin: 30, ResourceIDs: []string{"r2"}}, jan14(t, 10, 0))
	if e.ResourceID != "r2" || e.Start != jan14(t, 10, 30) {
		t.Fatalf("%+v", e)
	}
}

func TestWalkIn_Overrun(t *testing.T) {
	rs := []Resource{res("r1", 540, 1080)}
	ex := []Appointment{appt("i", "r1", InService, jan14(t, 9, 30), 30, 0)}
	e := est(t, rs, ex, nil, Walkin{DurationMin: 15}, jan14(t, 10, 7))
	if !e.OK || e.Start != jan14(t, 10, 12) {
		t.Fatalf("%+v", e)
	}
}

func TestWalkIn_HoursAndHorizon(t *testing.T) {
	r := res("r1", 540, 1080)
	r.TimeOff = []TimeOff{{From: jan14(t, 0, 0), To: jan14(t, 13, 0)}}
	e := est(t, []Resource{r}, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 0))
	if e.Start != jan14(t, 13, 0) {
		t.Fatalf("%+v", e)
	}
	e = est(t, []Resource{res("r1", 540, 1080)}, nil, nil, Walkin{DurationMin: 30}, jan14(t, 18, 30))
	if e.Start != lt(t, 2026, time.January, 15, 9, 0) {
		t.Fatalf("%+v", e)
	}
	// nothing within 8 days
	r2 := res("r1", 540, 1080)
	r2.TimeOff = []TimeOff{{From: jan14(t, 0, 0), To: jan14(t, 0, 0).AddDate(0, 0, 30)}}
	if e := est(t, []Resource{r2}, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 0)); e.OK {
		t.Fatalf("%+v", e)
	}
	// bad inputs and inactive resources
	if e := est(t, []Resource{r2}, nil, nil, Walkin{DurationMin: 0}, jan14(t, 10, 0)); e.OK {
		t.Fatal("zero duration")
	}
	ri := res("r1", 540, 1080)
	ri.Active = false
	if e := est(t, []Resource{ri}, nil, nil, Walkin{DurationMin: 30}, jan14(t, 10, 0)); e.OK {
		t.Fatal("inactive")
	}
	// too long for any window
	if e := est(t, []Resource{res("r1", 540, 600)}, nil, nil, Walkin{DurationMin: 120}, jan14(t, 8, 0)); e.OK {
		t.Fatal("too long")
	}
}

func TestWalkIn_DSTGap(t *testing.T) {
	r := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{time.Sunday: {{30, 240}}}}
	now := time.Date(2026, 3, 29, 0, 50, 0, 0, time.UTC)
	e := est(t, []Resource{r}, nil, nil, Walkin{DurationMin: 15}, now)
	if !e.OK || e.Start != now {
		t.Fatalf("%+v", e)
	}
	// busy until 01:00Z: next start is local 02:00 BST, never inside the gap
	ex := []Appointment{appt("x", "r1", Booked, now, 10, 0)}
	e = est(t, []Resource{r}, ex, nil, Walkin{DurationMin: 15}, now)
	if l := e.Start.In(london(t)); l.Format("15:04") != "02:00" {
		t.Fatalf("start %v", l)
	}
}
