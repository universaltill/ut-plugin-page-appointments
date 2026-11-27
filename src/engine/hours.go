package engine

import (
	"sort"
	"time"
)

type span struct{ start, end time.Time }

func minutes(n int) time.Duration { return time.Duration(n) * time.Minute }

// overlaps reports whether the half-open intervals [as,ae) and [bs,be) share
// any instant.
func overlaps(as, ae, bs, be time.Time) bool { return as.Before(be) && bs.Before(ae) }

// windowsOn returns the resource's working windows for a civil date as UTC
// spans, sorted by start. Each endpoint goes through time.Date in the shop
// zone, so a window across a DST change has its real elapsed length.
// Invalid windows are ignored.
func windowsOn(loc *time.Location, day Date, res Resource) []span {
	wd := time.Date(day.Y, day.M, day.D, 12, 0, 0, 0, loc).Weekday()
	src := res.WorkingHours[wd]
	ws := make([]Window, 0, len(src))
	for _, w := range src {
		if w.StartMin >= 0 && w.StartMin < w.EndMin && w.EndMin <= 1440 {
			ws = append(ws, w)
		}
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].StartMin < ws[j].StartMin })
	out := make([]span, 0, len(ws))
	for _, w := range ws {
		out = append(out, span{
			start: time.Date(day.Y, day.M, day.D, w.StartMin/60, w.StartMin%60, 0, 0, loc).UTC(),
			end:   time.Date(day.Y, day.M, day.D, w.EndMin/60, w.EndMin%60, 0, 0, loc).UTC(),
		})
	}
	return out
}

// onTimeOff reports whether [s,e) overlaps any time off of the resource.
func onTimeOff(res Resource, s, e time.Time) bool {
	for _, to := range res.TimeOff {
		if overlaps(s, e, to.From, to.To) {
			return true
		}
	}
	return false
}

// civilDate is the date of an instant in the shop zone.
func civilDate(loc *time.Location, t time.Time) Date {
	y, m, d := t.In(loc).Date()
	return Date{y, m, d}
}
