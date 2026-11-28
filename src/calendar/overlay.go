package calendar

import (
	"errors"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// overlayRows is how many upcoming bookings the sell-screen overlay lists.
const overlayRows = 8

// todayDocument is the sell-screen overlay "Today": the next bookings across
// all staff with a one-tap check-in, the number of walk-ins waiting and a
// button to the full calendar. It is small on purpose: the overlay shares the
// screen with the sale and never blocks it.
func (e *env) todayDocument(lead ...Component) (*Document, error) {
	from, to := e.bounds(e.today())
	bs, err := e.deps.Store.Bookings(from, to)
	if err != nil {
		return nil, err
	}
	sortBookings(bs)
	doc := &Document{Version: 1, Title: Key("appt.cal.today_title")}
	doc.Components = append(doc.Components, lead...)
	var up []Booking
	for _, b := range bs {
		if actionable(b.State) && b.State != engine.NoShow {
			up = append(up, b)
		}
	}
	q, err := e.deps.Store.Queue()
	if err != nil {
		return nil, err
	}
	if len(up) == 0 {
		doc.Components = append(doc.Components, emptyState(Key("appt.cal.today_empty"), Key("appt.cal.today_empty_body")))
	} else {
		shown := up
		if len(shown) > overlayRows {
			shown = shown[:overlayRows]
		}
		rows := make([][]any, len(shown))
		for i, b := range shown {
			rows[i] = []any{e.hm(b.Start), e.clientName(b), e.staffName(b.ResourceID), e.t.s("appt.cal.state." + string(b.State))}
		}
		doc.Components = append(doc.Components, table(
			[]Component{Key("appt.cal.col_time"), Key("appt.cal.col_client"), Key("appt.cal.staff"), Key("appt.cal.col_status")},
			[]string{"text", "text", "text", "text"}, rows))
		for _, b := range shown {
			if tk := token(b.ID); tk != "" && (b.State == engine.Booked || b.State == engine.Confirmed) {
				doc.Components = append(doc.Components, button("ci_"+tk, e.t.lit("appt.cal.ci_button", e.hm(b.Start), e.clientName(b)), "primary"))
			}
		}
		if len(up) > len(shown) {
			doc.Components = append(doc.Components, para(e.t.lit("appt.cal.today_more", len(up)-len(shown))))
		}
	}
	if len(q) > 0 {
		doc.Components = append(doc.Components, para(e.t.lit("appt.cal.today_waiting", len(q))))
	}
	doc.Components = append(doc.Components, button("opencal", Key("appt.cal.open_calendar"), "secondary"))
	return doc, nil
}

// quickCheckIn checks a client in from the overlay, one tap, no confirmation:
// a mis-tap is undone from the calendar.
func (e *env) quickCheckIn(tk string) (*Document, error) {
	b, ok, err := e.load1(tk)
	if err != nil {
		return nil, err
	}
	if !ok {
		return e.todayDocument(e.errNotice(errGone))
	}
	next, ev, err := engine.Transition(b.Appointment, engine.TransitionInput{
		To: engine.CheckedIn, ActorUserID: e.op.ActorID(), Now: e.now}, e.policy, e.deps.IDs)
	if err != nil {
		if errors.Is(err, engine.ErrTransitionNotAllowed) || isUserError(err) {
			return e.todayDocument(e.errNotice(err))
		}
		return nil, err
	}
	nb := Booking{Appointment: next, FirstName: b.FirstName}
	if err := e.deps.Store.Commit(Change{Update: &nb, Events: []engine.Event{ev}}); err != nil {
		return nil, err
	}
	return e.todayDocument(notice("info", e.t.lit("appt.cal.done", e.t.s("appt.cal.v.checkin"), e.clientName(b))))
}
