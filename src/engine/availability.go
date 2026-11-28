package engine

import (
	"sort"
	"time"
)

// Slot is a bookable start. End is the service end (buffer excluded).
type Slot struct {
	Start, End time.Time
	ResourceID string
}

// Request is the set of services being booked; Lines[i] belongs to Services[i].
type Request struct {
	Services []Service
	Lines    []Line
}

// shape validates the request and returns total duration and trailing buffer.
func (r Request) shape() (dur, buf int, err error) {
	if len(r.Services) == 0 {
		return 0, 0, ErrNoServices
	}
	if len(r.Services) != len(r.Lines) {
		return 0, 0, ErrInvalid
	}
	for _, l := range r.Lines {
		if l.DurationMin <= 0 || l.BufferMin < 0 {
			return 0, 0, ErrInvalid
		}
		dur += l.DurationMin
	}
	for _, s := range r.Services {
		if !s.Active {
			return 0, 0, ErrInactiveService
		}
	}
	return dur, r.Lines[len(r.Lines)-1].BufferMin, nil
}

func (r Request) eligible(resID string) bool {
	for _, s := range r.Services {
		if s.All {
			continue
		}
		ok := false
		for _, id := range s.ResourceIDs {
			if id == resID {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// Availability lists the bookable slots of one resource on a civil date.
// Error precedence: invalid policy, no services, invalid request, inactive
// service, resource not eligible; an inactive resource yields no slots.
func Availability(loc *time.Location, day Date, res Resource, req Request,
	existing []Appointment, p Policy, now time.Time) ([]Slot, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	dur, buf, err := req.shape()
	if err != nil {
		return nil, err
	}
	if !req.eligible(res.ID) {
		return nil, ErrResourceNotEligible
	}
	return slotsFor(loc, day, res, dur, buf, existing, p, now), nil
}

func slotsFor(loc *time.Location, day Date, res Resource, dur, buf int,
	existing []Appointment, p Policy, now time.Time) []Slot {
	out := []Slot{}
	if !res.Active {
		return out
	}
	var holds []span
	for _, a := range existing {
		if a.ResourceID == res.ID && a.State.Holds() {
			holds = append(holds, span{a.Start, a.holdEnd()})
		}
	}
	for _, w := range windowsOn(loc, day, res) {
		for s := w.start; ; s = s.Add(minutes(p.SlotStepMin)) {
			e := s.Add(minutes(dur))
			if e.After(w.end) {
				break
			}
			if s.Before(now) || onTimeOff(res, s, e) {
				continue
			}
			ce := e.Add(minutes(buf))
			free := true
			for _, h := range holds {
				if overlaps(s, ce, h.start, h.end) {
					free = false
					break
				}
			}
			if free {
				out = append(out, Slot{Start: s, End: e, ResourceID: res.ID})
			}
		}
	}
	return out
}

// AvailabilityAny merges the slots of every active, eligible resource, sorted
// by start then resource id.
func AvailabilityAny(loc *time.Location, day Date, resources []Resource, req Request,
	existing []Appointment, p Policy, now time.Time) ([]Slot, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	dur, buf, err := req.shape()
	if err != nil {
		return nil, err
	}
	out := []Slot{}
	for _, r := range resources {
		if !req.eligible(r.ID) {
			continue
		}
		out = append(out, slotsFor(loc, day, r, dur, buf, existing, p, now)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].ResourceID < out[j].ResourceID
	})
	return out, nil
}
