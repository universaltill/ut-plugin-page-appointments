package calendar

import (
	"fmt"
	"sort"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// maxOpenButtons caps the per-booking buttons so a very busy day stays inside
// the view format's 200-component limit; a notice says when it bites.
const maxOpenButtons = 60

// actionable reports whether a booking still has something to do at the desk.
// A no-show stays actionable so a mistaken tap can be undone.
func actionable(s engine.State) bool {
	return s.Holds() || s == engine.NoShow
}

func (e *env) stateLabel(b Booking) string {
	s := e.t.s("appt.cal.state." + string(b.State))
	if b.WalkIn {
		s += " (" + e.t.s("appt.cal.tag.walkin") + ")"
	}
	if b.Source == "provider" {
		s += " (" + e.t.s("appt.cal.tag.provider") + ")"
	}
	if b.LateCancel {
		s += " (" + e.t.s("appt.cal.tag.late") + ")"
	}
	return s
}

func (e *env) hoursText(s Staff, d engine.Date) string {
	wd := time.Date(d.Y, d.M, d.D, 12, 0, 0, 0, time.UTC).Weekday()
	ws := append([]engine.Window(nil), s.WorkingHours[wd]...)
	sort.Slice(ws, func(i, j int) bool { return ws[i].StartMin < ws[j].StartMin })
	if len(ws) == 0 {
		return e.t.s("appt.cal.not_working")
	}
	out := ""
	for i, w := range ws {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%02d:%02d-%02d:%02d", w.StartMin/60, w.StartMin%60, w.EndMin/60, w.EndMin%60)
	}
	return e.t.s("appt.cal.working", out)
}

func (e *env) timeOffText(s Staff, from, to time.Time) string {
	for _, off := range s.TimeOff {
		if off.From.Before(to) && from.Before(off.To) {
			r := off.Reason
			if r != "holiday" && r != "away" {
				r = "other"
			}
			return e.t.s("appt.cal.timeoff", e.t.s("appt.cal.timeoff."+r))
		}
	}
	return ""
}

// navigation is the date strip and the "new booking" entry.
func (e *env) navigation(d engine.Date) []Component {
	return []Component{
		heading(Lit(e.dateLabel(d))),
		button("day_"+compactDate(addDays(d, -1)), Key("appt.cal.prev_day"), "secondary"),
		button("today", Key("appt.cal.today"), "secondary"),
		button("day_"+compactDate(addDays(d, 1)), Key("appt.cal.next_day"), "secondary"),
		button("new", Key("appt.cal.new_booking"), "primary"),
	}
}

// dayDocument draws one civil day: a table per staff member, the buttons to
// open each booking, and the walk-in queue. lead components (a result or
// error notice) come first.
func (e *env) dayDocument(d engine.Date, lead ...Component) (*Document, error) {
	from, to := e.bounds(d)
	bs, err := e.deps.Store.Bookings(from, to)
	if err != nil {
		return nil, err
	}
	sortBookings(bs)
	doc := &Document{Version: 1, Title: Key("appt.cal.title")}
	doc.Components = append(doc.Components, lead...)
	doc.Components = append(doc.Components, e.navigation(d)...)

	dbl := overlapping(bs)
	if len(dbl) > 0 {
		doc.Components = append(doc.Components, notice("warn", e.t.lit("appt.cal.double_booked", len(dbl))))
	}
	staff := append([]Staff(nil), e.staff...)
	sort.SliceStable(staff, func(i, j int) bool {
		if staff[i].Name != staff[j].Name {
			return staff[i].Name < staff[j].Name
		}
		return staff[i].ID < staff[j].ID
	})
	if len(staff) == 0 {
		doc.Components = append(doc.Components, emptyState(Key("appt.cal.no_staff"), Key("appt.cal.no_staff_body")))
	}
	opened := 0
	skipped := 0
	for _, s := range staff {
		if !s.Active {
			continue
		}
		doc.Components = append(doc.Components, heading(Lit(s.Name)), para(Lit(e.hoursText(s, d))))
		if off := e.timeOffText(s, from, to); off != "" {
			doc.Components = append(doc.Components, notice("info", Lit(off)))
		}
		var rows [][]any
		var mine []Booking
		for _, b := range bs {
			if b.ResourceID == s.ID {
				mine = append(mine, b)
			}
		}
		if len(mine) == 0 {
			doc.Components = append(doc.Components, emptyState(Key("appt.cal.day_empty"), Key("appt.cal.day_empty_body")))
			continue
		}
		for _, b := range mine {
			status := e.stateLabel(b)
			if dbl[b.ID] {
				status += " (" + e.t.s("appt.cal.tag.overlap") + ")"
			}
			rows = append(rows, []any{e.span(b), e.clientName(b), e.serviceNames(b), status})
		}
		doc.Components = append(doc.Components, table(
			[]Component{Key("appt.cal.col_time"), Key("appt.cal.col_client"), Key("appt.cal.col_service"), Key("appt.cal.col_status")},
			[]string{"text", "text", "text", "text"}, rows))
		for _, b := range mine {
			tk := token(b.ID)
			if !actionable(b.State) || tk == "" {
				continue
			}
			if opened >= maxOpenButtons {
				skipped++
				continue
			}
			opened++
			doc.Components = append(doc.Components,
				button("open_"+tk, Lit(e.hm(b.Start)+" "+e.clientName(b)), "secondary"))
		}
	}
	if skipped > 0 {
		doc.Components = append(doc.Components, notice("warn", e.t.lit("appt.cal.too_many", maxOpenButtons)))
	}
	q, err := e.queuePanel()
	if err != nil {
		return nil, err
	}
	doc.Components = append(doc.Components, q...)
	return doc, nil
}
