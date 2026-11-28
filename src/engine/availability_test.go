package engine

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

var jan14Date = Date{2026, time.January, 14}

func avail(t *testing.T, r Resource, rq Request, ex []Appointment, now time.Time) []Slot {
	t.Helper()
	s, err := Availability(london(t), jan14Date, r, rq, ex, DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func excluded(t *testing.T, all, got []Slot) []string {
	t.Helper()
	in := map[time.Time]bool{}
	for _, s := range got {
		in[s.Start] = true
	}
	var out []string
	for _, s := range all {
		if !in[s.Start] {
			out = append(out, s.Start.Format("15:04"))
		}
	}
	return out
}

func TestAvailability_Hours(t *testing.T) {
	s := avail(t, res("r1", 540, 720), req(30, 0), nil, jan14(t, 8, 0))
	if len(s) != 11 || hm(s, time.UTC)[0] != "09:00" || hm(s, time.UTC)[10] != "11:30" {
		t.Fatalf("got %d slots %v", len(s), hm(s, time.UTC))
	}
	if s[0].End != jan14(t, 9, 30) || s[0].ResourceID != "r1" {
		t.Fatalf("slot shape %+v", s[0])
	}
}

func TestAvailability_Buffer(t *testing.T) {
	r := res("r1", 540, 720)
	base := avail(t, r, req(30, 0), nil, jan14(t, 8, 0))
	ex := []Appointment{appt("a", "r1", Booked, jan14(t, 10, 0), 30, 10)}
	got := avail(t, r, req(30, 0), ex, jan14(t, 8, 0))
	want := []string{"09:45", "10:00", "10:15", "10:30"}
	if e := excluded(t, base, got); !reflect.DeepEqual(e, want) {
		t.Fatalf("excluded %v want %v", e, want)
	}
}

func TestAvailability_CandidateBuffer(t *testing.T) {
	r := res("r1", 540, 720)
	ex := []Appointment{appt("a", "r1", Booked, jan14(t, 10, 0), 30, 10)}
	got := hm(avail(t, r, req(30, 15), ex, jan14(t, 8, 0)), time.UTC)
	has := func(x string) bool {
		for _, g := range got {
			if g == x {
				return true
			}
		}
		return false
	}
	if !has("09:15") || has("09:30") {
		t.Fatalf("slots %v", got)
	}
}

func TestAvailability_HoldingStates(t *testing.T) {
	r := res("r1", 540, 720)
	cases := map[State]bool{Booked: true, Confirmed: true, CheckedIn: true, InService: true,
		Completed: false, CancelledByClient: false, CancelledByShop: false, NoShow: false}
	base := avail(t, r, req(30, 0), nil, jan14(t, 8, 0))
	for st, blocks := range cases {
		t.Run(string(st), func(t *testing.T) {
			ex := []Appointment{appt("a", "r1", st, jan14(t, 10, 0), 30, 0)}
			got := avail(t, r, req(30, 0), ex, jan14(t, 8, 0))
			if blocks != (len(got) < len(base)) {
				t.Fatalf("blocks=%v but %d vs %d slots", blocks, len(got), len(base))
			}
		})
	}
}

func TestAvailability_TimeOff(t *testing.T) {
	r := res("r1", 540, 720)
	base := avail(t, r, req(30, 0), nil, jan14(t, 8, 0))
	r.TimeOff = []TimeOff{{From: jan14(t, 10, 0), To: jan14(t, 11, 0), Reason: "x"}}
	got := avail(t, r, req(30, 0), nil, jan14(t, 8, 0))
	want := []string{"09:45", "10:00", "10:15", "10:30", "10:45"}
	if e := excluded(t, base, got); !reflect.DeepEqual(e, want) {
		t.Fatalf("excluded %v want %v", e, want)
	}
	r.TimeOff = []TimeOff{{From: jan14(t, 0, 0), To: jan14(t, 23, 59)}}
	if got := avail(t, r, req(30, 0), nil, jan14(t, 8, 0)); len(got) != 0 {
		t.Fatalf("whole-day time off: %d slots", len(got))
	}
}

func TestAvailability_DSTSpringGap(t *testing.T) {
	loc := london(t)
	r := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{time.Sunday: {{30, 240}}}}
	d := Date{2026, time.March, 29}
	now := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	s, err := Availability(loc, d, r, req(15, 0), nil, DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 10 {
		t.Fatalf("got %d slots, want 10", len(s))
	}
	if s[0].Start != time.Date(2026, 3, 29, 0, 30, 0, 0, time.UTC) || s[9].Start != time.Date(2026, 3, 29, 2, 45, 0, 0, time.UTC) {
		t.Fatalf("first/last %v %v", s[0].Start, s[9].Start)
	}
	for _, sl := range s {
		l := sl.Start.In(loc)
		if l.Hour() == 1 {
			t.Fatalf("slot in the gap: %v", l)
		}
	}
	if got := s[2].Start.In(loc).Format("15:04"); got != "02:00" {
		t.Fatalf("first slot after gap = %s", got)
	}
	// 30 minute service at 00:45Z spans the changeover.
	s30, _ := Availability(loc, d, r, req(30, 0), nil, DefaultPolicy(), now)
	found := false
	for _, sl := range s30 {
		if sl.Start == time.Date(2026, 3, 29, 0, 45, 0, 0, time.UTC) {
			found = true
		}
	}
	if !found {
		t.Fatal("30 min service at 00:45Z should be allowed")
	}
}

func TestAvailability_DSTAutumnRepeat(t *testing.T) {
	loc := london(t)
	r := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{time.Sunday: {{30, 180}}}}
	d := Date{2026, time.October, 25}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s, err := Availability(loc, d, r, req(15, 0), nil, DefaultPolicy(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 14 {
		t.Fatalf("got %d, want 14", len(s))
	}
	if s[0].Start != time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC) || s[13].Start != time.Date(2026, 10, 25, 2, 45, 0, 0, time.UTC) {
		t.Fatalf("first/last %v %v", s[0].Start, s[13].Start)
	}
	labels := map[string]int{}
	for i, sl := range s {
		labels[sl.Start.In(loc).Format("15:04")]++
		if i > 0 && !sl.Start.After(s[i-1].Start) {
			t.Fatal("not ordered by instant")
		}
	}
	for _, l := range []string{"01:00", "01:15", "01:30", "01:45"} {
		if labels[l] != 2 {
			t.Fatalf("label %s occurs %d times", l, labels[l])
		}
	}
}

func TestAvailability_DSTNormalDay(t *testing.T) {
	loc := london(t)
	for _, d := range []Date{{2026, time.March, 29}, {2026, time.October, 25}} {
		r := res("r1", 540, 1020)
		now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		s, err := Availability(loc, d, r, req(15, 0), nil, DefaultPolicy(), now)
		if err != nil || len(s) != 32 {
			t.Fatalf("%v: %d slots, %v", d, len(s), err)
		}
	}
}

func TestAvailability_Past(t *testing.T) {
	r := res("r1", 540, 720)
	s := avail(t, r, req(30, 0), nil, jan14(t, 10, 7))
	if hm(s, time.UTC)[0] != "10:15" {
		t.Fatalf("first %v", hm(s, time.UTC)[0])
	}
	s = avail(t, r, req(30, 0), nil, jan14(t, 10, 15))
	if hm(s, time.UTC)[0] != "10:15" {
		t.Fatalf("first %v", hm(s, time.UTC)[0])
	}
}

func TestAvailability_MultiService(t *testing.T) {
	a := Service{SKU: "a", DurationMin: 30, BufferMin: 5, All: true, Active: true}
	b := Service{SKU: "b", DurationMin: 45, BufferMin: 10, All: true, Active: true}
	rq := Request{Services: []Service{a, b}, Lines: []Line{
		{SKU: "a", DurationMin: 30, BufferMin: 5}, {SKU: "b", DurationMin: 45, BufferMin: 10}}}
	s := avail(t, res("r1", 540, 660), rq, nil, jan14(t, 8, 0))
	got := hm(s, time.UTC)
	if got[len(got)-1] != "09:45" || len(got) != 4 {
		t.Fatalf("slots %v", got)
	}
	if s[0].End != jan14(t, 10, 15) {
		t.Fatalf("end %v (buffer excluded)", s[0].End)
	}
	// hold of the candidate is 85 min: an existing booking at 10:25 blocks 09:00..
	ex := []Appointment{appt("x", "r1", Booked, jan14(t, 10, 30), 10, 0)}
	got2 := hm(avail(t, res("r1", 540, 660), rq, ex, jan14(t, 8, 0)), time.UTC)
	if len(got2) != 1 || got2[0] != "09:00" {
		t.Fatalf("slots %v", got2)
	}
}

func TestAvailability_SplitShift(t *testing.T) {
	r := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{
		time.Wednesday: {{840, 1080}, {540, 720}}}} // deliberately unsorted
	got := hm(avail(t, r, req(30, 0), nil, jan14(t, 8, 0)), time.UTC)
	for _, g := range got {
		if g == "11:45" {
			t.Fatal("11:45 should not exist")
		}
	}
	if got[len(got)-1] != "17:30" {
		t.Fatalf("last %v", got[len(got)-1])
	}
	found := false
	for i, g := range got {
		if g == "14:00" && got[i-1] == "11:30" {
			found = true
		}
	}
	if !found {
		t.Fatalf("slots %v", got)
	}
	// Re-anchoring: afternoon window starting at 14:10 gives 14:10, 14:25 ...
	r.WorkingHours[time.Wednesday] = []Window{{540, 720}, {850, 1080}}
	got = hm(avail(t, r, req(30, 0), nil, jan14(t, 8, 0)), time.UTC)
	if got[11] != "14:10" {
		t.Fatalf("slots %v", got)
	}
}

func TestAvailability_AnyStaff(t *testing.T) {
	r1, r2 := res("r1", 540, 720), res("r2", 540, 720)
	ex := []Appointment{appt("a", "r1", Booked, jan14(t, 9, 0), 60, 0)}
	s, err := AvailabilityAny(london(t), jan14Date, []Resource{r2, r1}, req(30, 0), ex, DefaultPolicy(), jan14(t, 8, 0))
	if err != nil {
		t.Fatal(err)
	}
	if s[0].Start != jan14(t, 9, 0) || s[0].ResourceID != "r2" {
		t.Fatalf("first %+v", s[0])
	}
	var at10 []string
	for _, x := range s {
		if x.Start == jan14(t, 10, 0) {
			at10 = append(at10, x.ResourceID)
		}
	}
	if !reflect.DeepEqual(at10, []string{"r1", "r2"}) {
		t.Fatalf("10:00 -> %v", at10)
	}
	// inactive and ineligible resources are skipped
	r3 := res("r3", 540, 720)
	r3.Active = false
	rq := req(30, 0)
	rq.Services[0].All = false
	rq.Services[0].ResourceIDs = []string{"r1"}
	s, _ = AvailabilityAny(london(t), jan14Date, []Resource{r1, r2, r3}, rq, nil, DefaultPolicy(), jan14(t, 8, 0))
	for _, x := range s {
		if x.ResourceID != "r1" {
			t.Fatalf("slot on %s", x.ResourceID)
		}
	}
	if _, err := AvailabilityAny(london(t), jan14Date, nil, Request{}, nil, DefaultPolicy(), jan14(t, 8, 0)); !errors.Is(err, ErrNoServices) {
		t.Fatal(err)
	}
}

func TestAvailability_Errors(t *testing.T) {
	loc, r, now := london(t), res("r1", 540, 720), jan14(t, 8, 0)
	run := func(rr Resource, rq Request, p Policy) ([]Slot, error) {
		return Availability(loc, jan14Date, rr, rq, nil, p, now)
	}
	if _, err := run(r, Request{}, DefaultPolicy()); !errors.Is(err, ErrNoServices) {
		t.Fatalf("no services: %v", err)
	}
	rq := req(30, 0)
	rq.Services[0].Active = false
	if _, err := run(r, rq, DefaultPolicy()); !errors.Is(err, ErrInactiveService) {
		t.Fatalf("inactive: %v", err)
	}
	rq = req(30, 0)
	rq.Services[0].All = false
	rq.Services[0].ResourceIDs = []string{"other"}
	if _, err := run(r, rq, DefaultPolicy()); !errors.Is(err, ErrResourceNotEligible) {
		t.Fatalf("eligible: %v", err)
	}
	rq.Services[0].ResourceIDs = []string{"r1"}
	if s, err := run(r, rq, DefaultPolicy()); err != nil || len(s) == 0 {
		t.Fatalf("listed resource: %v %d", err, len(s))
	}
	ri := r
	ri.Active = false
	if s, err := run(ri, req(30, 0), DefaultPolicy()); err != nil || len(s) != 0 {
		t.Fatalf("inactive resource: %v %v", s, err)
	}
	p := DefaultPolicy()
	p.SlotStepMin = 7
	if _, err := run(r, req(30, 0), p); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("policy: %v", err)
	}
	rq = req(30, 0)
	rq.Lines = nil
	if _, err := run(r, rq, DefaultPolicy()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatch: %v", err)
	}
	rq = req(0, 0)
	if _, err := run(r, rq, DefaultPolicy()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duration: %v", err)
	}
	// weekday without hours: empty
	r2 := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{}}
	if s, err := run(r2, req(30, 0), DefaultPolicy()); err != nil || len(s) != 0 {
		t.Fatal("no hours should be empty")
	}
	// invalid window ignored
	r3 := Resource{ID: "r1", Active: true, WorkingHours: map[time.Weekday][]Window{time.Wednesday: {{720, 540}}}}
	if s, _ := run(r3, req(30, 0), DefaultPolicy()); len(s) != 0 {
		t.Fatal("bad window should yield nothing")
	}
}

func TestAvailability_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	loc := london(t)
	p := DefaultPolicy()
	now := jan14(t, 7, 0)
	for i := 0; i < 200; i++ {
		r := res("r1", 540, 1020)
		var ex []Appointment
		for k := 0; k < rng.Intn(5); k++ {
			st := []State{Booked, Confirmed, Completed, NoShow}[rng.Intn(4)]
			ex = append(ex, appt("e"+string(rune('a'+k)), "r1", st,
				jan14(t, 9, 0).Add(time.Duration(rng.Intn(480))*time.Minute), 15+rng.Intn(60), rng.Intn(15)))
		}
		if rng.Intn(3) == 0 {
			from := jan14(t, 9, 0).Add(time.Duration(rng.Intn(400)) * time.Minute)
			r.TimeOff = []TimeOff{{From: from, To: from.Add(time.Duration(10+rng.Intn(90)) * time.Minute)}}
		}
		dur, buf := 15+rng.Intn(5)*15, rng.Intn(3)*5
		rq := req(dur, buf)
		got, err := Availability(loc, jan14Date, r, rq, ex, p, now)
		if err != nil {
			t.Fatal(err)
		}
		in := map[time.Time]bool{}
		for _, s := range got {
			in[s.Start] = true
			cand := Appointment{ID: "new", ResourceID: "r1", Start: s.Start, End: s.End, BufferMin: buf, State: Booked}
			if _, err := CheckConflict(cand, ex, p, nil, fakeClock{now}, counterIDs("e")); err != nil {
				t.Fatalf("returned slot %v conflicts", s.Start)
			}
			for _, to := range r.TimeOff {
				if s.Start.Before(to.To) && to.From.Before(s.End) {
					t.Fatalf("slot %v overlaps time off", s.Start)
				}
			}
		}
		for s := jan14(t, 9, 0); !s.Add(time.Duration(dur) * time.Minute).After(jan14(t, 17, 0)); s = s.Add(15 * time.Minute) {
			if in[s] {
				continue
			}
			cand := Appointment{ID: "new", ResourceID: "r1", Start: s, End: s.Add(time.Duration(dur) * time.Minute), BufferMin: buf, State: Booked}
			_, cerr := CheckConflict(cand, ex, p, nil, fakeClock{now}, counterIDs("e"))
			off := false
			for _, to := range r.TimeOff {
				if s.Before(to.To) && to.From.Before(cand.End) {
					off = true
				}
			}
			if cerr == nil && !off {
				t.Fatalf("slot %v wrongly missing", s)
			}
		}
	}
}

// A slot that overshoots the working window by less than one step is refused:
// 17 minutes on a 15 minute grid in 09:00-10:00 cannot start at 09:45.
func TestAvailability_SlotOvershootingWindowByLessThanAStep(t *testing.T) {
	for _, dur := range []int{17, 25} {
		s := avail(t, res("r1", 540, 600), req(dur, 0), nil, jan14(t, 8, 0))
		got := hm(s, time.UTC)
		want := []string{"09:00", "09:15", "09:30"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("dur %d: got %v want %v", dur, got, want)
		}
	}
}
