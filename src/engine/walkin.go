package engine

import (
	"sort"
	"time"
)

// Walkin is a queued walk-in client.
type Walkin struct {
	ID                     string
	Seq                    int64
	Arrived                time.Time
	DurationMin, BufferMin int
	ResourceIDs            []string // empty: any resource
}

// Estimate is the nearest-free answer for a walk-in.
type Estimate struct {
	OK         bool
	ResourceID string
	Start      time.Time
}

// OrderQueue returns a copy ordered by arrival, then Seq, then ID.
func OrderQueue(q []Walkin) []Walkin {
	out := append([]Walkin(nil), q...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if !a.Arrived.Equal(b.Arrived) {
			return a.Arrived.Before(b.Arrived)
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		return a.ID < b.ID
	})
	return out
}

const horizonDays = 8

func (w Walkin) allows(id string) bool {
	if len(w.ResourceIDs) == 0 {
		return true
	}
	for _, r := range w.ResourceIDs {
		if r == id {
			return true
		}
	}
	return false
}

type resHold struct {
	resID string
	span
}

// ceilMinute rounds t up to a whole minute.
func ceilMinute(t time.Time) time.Time {
	r := t.Truncate(time.Minute)
	if r.Before(t) {
		r = r.Add(time.Minute)
	}
	return r
}

// EstimateNearestFree finds where and when next could start: the queue ahead
// is placed greedily in order, then next gets its earliest feasible start
// (minute resolution, now rounded up). The search covers 8 civil days. An
// in_service appointment already past its planned end is treated as running
// until now + OverrunMin.
func EstimateNearestFree(loc *time.Location, resources []Resource, existing []Appointment,
	queueAhead []Walkin, next Walkin, p Policy, now time.Time) Estimate {
	now = ceilMinute(now.UTC())
	var holds []resHold
	for _, a := range Current(existing) {
		if !a.State.Holds() {
			continue
		}
		end := a.holdEnd()
		if a.State == InService && !end.After(now) {
			end = now.Add(minutes(p.OverrunMin))
		}
		holds = append(holds, resHold{a.ResourceID, span{a.Start, end}})
	}
	for _, w := range OrderQueue(queueAhead) {
		if resID, start, ok := earliest(loc, resources, holds, w, now); ok {
			holds = append(holds, resHold{resID, span{start, start.Add(minutes(w.DurationMin + w.BufferMin))}})
		}
	}
	resID, start, ok := earliest(loc, resources, holds, next, now)
	if !ok {
		return Estimate{}
	}
	return Estimate{OK: true, ResourceID: resID, Start: start}
}

// earliest returns the best (start, then lowest resource id) feasible
// placement of w. Feasible starts can only begin at now, a hold end, a window
// start or a time-off end, so only those are tried.
func earliest(loc *time.Location, resources []Resource, holds []resHold, w Walkin, now time.Time) (string, time.Time, bool) {
	if w.DurationMin <= 0 || w.BufferMin < 0 {
		return "", time.Time{}, false
	}
	today := civilDate(loc, now)
	horizon := time.Date(today.Y, today.M, today.D+horizonDays, 0, 0, 0, 0, loc).UTC()
	bestRes, bestStart, found := "", time.Time{}, false
	for _, r := range resources {
		if !r.Active || !w.allows(r.ID) {
			continue
		}
		var wins []span
		for d := 0; d < horizonDays; d++ {
			day := time.Date(today.Y, today.M, today.D+d, 12, 0, 0, 0, loc)
			wins = append(wins, windowsOn(loc, civilDate(loc, day), r)...)
		}
		cands := []time.Time{now}
		var mine []span
		for _, h := range holds {
			if h.resID == r.ID {
				mine = append(mine, h.span)
				cands = append(cands, h.end)
			}
		}
		for _, wn := range wins {
			cands = append(cands, wn.start)
		}
		for _, to := range r.TimeOff {
			cands = append(cands, to.To)
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].Before(cands[j]) })
		for _, c := range cands {
			if c.Before(now) || !c.Before(horizon) {
				continue
			}
			if found && c.After(bestStart) {
				break
			}
			if feasible(r, wins, mine, c, w) {
				if !found || c.Before(bestStart) || r.ID < bestRes {
					bestRes, bestStart, found = r.ID, c, true
				}
				break
			}
		}
	}
	return bestRes, bestStart, found
}

func feasible(r Resource, wins, holds []span, s time.Time, w Walkin) bool {
	e := s.Add(minutes(w.DurationMin))
	inWin := false
	for _, wn := range wins {
		if !s.Before(wn.start) && !e.After(wn.end) {
			inWin = true
			break
		}
	}
	if !inWin || onTimeOff(r, s, e) {
		return false
	}
	ce := e.Add(minutes(w.BufferMin))
	for _, h := range holds {
		if overlaps(s, ce, h.start, h.end) {
			return false
		}
	}
	return true
}
