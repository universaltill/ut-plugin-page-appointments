package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// Errors of the calendar layer (wire codes map to appt.cal.err.<code>).
var (
	errActorRequired = errors.New("actor_required")
	errSlotTaken     = errors.New("slot_taken")
	errDatePast      = errors.New("date_past")
	errTimeInvalid   = errors.New("time_invalid")
	errProviderMove  = errors.New("provider_move")
	errNoStaff       = errors.New("no_staff")
)

const (
	dateChoices = 14  // days offered in the date select
	maxOptions  = 100 // select option limit of the view format
)

// bookingForm is one screen of the create or move flow.
type bookingForm struct {
	submit   string // action name that posts it
	two      bool   // second step: shows the time select
	moveID   string // set for a move: name and service are fixed
	staffID  string
	sku      string
	date     engine.Date
	name     string
	override bool // draw the manager override fields
	slots    []engine.Slot
	showAll  bool
}

func (e *env) bookableStaff() []Staff {
	var out []Staff
	for _, s := range e.staff {
		if s.Active && !s.ProviderMastered {
			out = append(out, s)
		}
	}
	return out
}

func (e *env) canOverride() bool { return e.policy.AllowDoubleBooking && e.op.IsManager() }

func (e *env) dateOptions(sel engine.Date) []Component {
	var out []Component
	start := e.today()
	seen := false
	for i := 0; i < dateChoices; i++ {
		d := addDays(start, i)
		if isoDate(d) == isoDate(sel) {
			seen = true
		}
		out = append(out, option(isoDate(d), Lit(e.dateLabel(d))))
	}
	if !seen {
		out = append(out, option(isoDate(sel), Lit(e.dateLabel(sel))))
	}
	return out
}

// slotLabels shows wall-clock times; on a clock-change day two slots can read
// the same, and then the zone abbreviation tells them apart.
func (e *env) slotOptions(slots []engine.Slot) ([]Component, bool) {
	count := map[string]int{}
	for _, s := range slots {
		count[e.hm(s.Start)]++
	}
	cut := false
	if len(slots) > maxOptions {
		slots, cut = slots[:maxOptions], true
	}
	out := make([]Component, len(slots))
	for i, s := range slots {
		label := e.hm(s.Start)
		if count[label] > 1 {
			zone, _ := s.Start.In(e.loc).Zone()
			label += " " + zone
		}
		out[i] = option(s.Start.UTC().Format(time.RFC3339), Lit(label))
	}
	return out, cut
}

func (e *env) form(f bookingForm) []Component {
	var out []Component
	staffOpts := make([]Component, 0, len(e.staff))
	for _, s := range e.bookableStaff() {
		staffOpts = append(staffOpts, option(s.ID, Lit(s.Name)))
	}
	var fields []Component
	fields = append(fields, field("staff", Key("appt.cal.staff"), "select", true, nonEmpty(f.staffID, staffOpts), staffOpts))
	if f.moveID == "" {
		svcOpts := e.activeServiceOptions()
		fields = append(fields, field("service", Key("appt.cal.service"), "select", true, nonEmpty(f.sku, svcOpts), svcOpts))
	}
	fields = append(fields, field("date", Key("appt.cal.date"), "select", true, isoDate(f.date), e.dateOptions(f.date)))
	if f.moveID == "" {
		fields = append(fields, field("first_name", Key("appt.cal.first_name_only"), "text", true, f.name, nil))
	}
	submit := Key("appt.cal.find_times")
	if f.two {
		opts, cut := e.slotOptions(f.slots)
		if len(opts) > 0 {
			fields = append(fields, field("time", Key("appt.cal.time"), "select", !f.override, nil, opts))
		}
		if cut {
			out = append(out, notice("info", e.t.lit("appt.cal.times_cut", maxOptions)))
		}
		if f.override {
			out = append(out, para(Key("appt.cal.override_hint")))
			fields = append(fields,
				field("override", Key("appt.cal.override_toggle"), "toggle", false, false, nil),
				field("override_time", Key("appt.cal.override_time"), "text", false, nil, nil),
				field("override_reason", Key("appt.cal.override_reason"), "text", false, nil, nil))
		}
		submit = Key("appt.cal.book")
		if f.moveID != "" {
			submit = Key("appt.cal.move_here")
		}
	}
	out = append(out, form(f.submit, submit, fields...),
		button("day_"+compactDate(f.date), Key("appt.cal.back"), "secondary"))
	return out
}

func nonEmpty(v string, opts []Component) any {
	for _, o := range opts {
		if o["value"] == v {
			return v
		}
	}
	return nil
}

// newBooking draws the first screen of the create flow.
func (e *env) newBooking(date engine.Date, lead ...Component) (*Document, error) {
	return e.newBookingWith(bookingForm{submit: "find", date: date}, lead...)
}

// newBookingWith is the same screen with what the operator already typed put
// back, so a refusal never costs them their entries.
func (e *env) newBookingWith(f bookingForm, lead ...Component) (*Document, error) {
	date := f.date
	if len(e.bookableStaff()) == 0 || len(e.activeServiceOptions()) == 0 {
		return e.dayDocument(date, e.errNotice(errNoStaff))
	}
	doc := &Document{Version: 1, Title: Key("appt.cal.new_booking")}
	doc.Components = append(doc.Components, lead...)
	doc.Components = append(doc.Components, heading(Key("appt.cal.new_booking")))
	doc.Components = append(doc.Components, e.form(f)...)
	return doc, nil
}

// request builds the engine request and the one line of a service.
func (e *env) request(svc ServiceInfo) (engine.Request, engine.Line) {
	l := engine.Line{SKU: svc.SKU, Name: svc.Name, Qty: 1, Amount: svc.Price,
		DurationMin: svc.DurationMin, BufferMin: svc.BufferMin}
	return engine.Request{Services: []engine.Service{svc.Service}, Lines: []engine.Line{l}}, l
}

// freeSlots lists the bookable starts of one staff member on a date, ignoring
// the row a move replaces.
func (e *env) freeSlots(staffID string, req engine.Request, date engine.Date, ignore string) ([]engine.Slot, error) {
	s, ok := e.staffByID(staffID)
	if !ok {
		return nil, engine.ErrInvalid
	}
	from, to := e.bounds(date)
	bs, err := e.deps.Store.Bookings(from, to)
	if err != nil {
		return nil, err
	}
	var existing []engine.Appointment
	for _, b := range bs {
		if b.ID != ignore {
			existing = append(existing, b.Appointment)
		}
	}
	return engine.Availability(e.loc, date, s.Resource, req, existing, e.policy, e.now)
}

type picked struct {
	staff   Staff
	svc     ServiceInfo
	date    engine.Date
	name    string
	moveOld *Booking
}

// pick reads and validates the fields both steps share. moveID is the booking
// being moved, or "".
func (e *env) pick(a Ask, moveID string) (picked, error) {
	var p picked
	var err error
	if moveID != "" {
		old, ok, err := e.deps.Store.Booking(moveID)
		if err != nil {
			return p, err
		}
		if !ok {
			return p, errGone
		}
		if old.Source != "own" {
			return p, errProviderMove
		}
		if len(old.Lines) != 1 {
			return p, engine.ErrInvalid
		}
		p.moveOld = &old
		svc, ok := e.serviceBySKU(old.Lines[0].SKU)
		if !ok {
			return p, engine.ErrInactiveService
		}
		p.svc, p.name = svc, old.FirstName
	} else {
		if p.name, err = CleanName(formStr(a.Form, "first_name"), false); err != nil {
			return p, err
		}
		svc, ok := e.serviceBySKU(formStr(a.Form, "service"))
		if !ok {
			return p, engine.ErrInvalid
		}
		if !svc.Active {
			return p, engine.ErrInactiveService
		}
		p.svc = svc
	}
	s, ok := e.staffByID(formStr(a.Form, "staff"))
	if !ok || !s.Active || s.ProviderMastered {
		return p, engine.ErrResourceNotEligible
	}
	p.staff = s
	d, ok := parseISODate(formStr(a.Form, "date"))
	if !ok {
		return p, engine.ErrInvalid
	}
	if before(d, e.today()) {
		return p, errDatePast
	}
	p.date = d
	return p, nil
}

func (p picked) formFor(submit string, moveID string) bookingForm {
	return bookingForm{submit: submit, moveID: moveID, staffID: p.staff.ID, sku: p.svc.SKU, date: p.date, name: p.name}
}

// findTimes is the first step: it validates the choice and shows the free
// times, or says there are none.
func (e *env) findTimes(a Ask, moveID string) (*Document, error) {
	date := e.dateOrToday(a)
	p, err := e.pick(a, moveID)
	if err != nil {
		return e.stepOneError(a, moveID, date, err)
	}
	slots, err := e.freeSlots(p.staff.ID, mustReq(e, p), p.date, moveID)
	if err != nil {
		return e.stepOneError(a, moveID, p.date, err)
	}
	if len(slots) == 0 && !e.canOverride() {
		return e.stepOne(a, moveID, p.date, notice("warn", e.t.lit("appt.cal.no_times", p.staff.Name, e.dateLabel(p.date))))
	}
	f := p.formFor(createAction(moveID), moveID)
	f.two, f.slots, f.override = true, slots, e.canOverride()
	return e.formDoc(moveID, p, f, notice("info", e.t.lit("appt.cal.times_for", p.staff.Name, e.dateLabel(p.date), len(slots))))
}

func createAction(moveID string) string {
	if moveID != "" {
		return "domove_" + token(moveID)
	}
	return "create"
}

func mustReq(e *env, p picked) engine.Request {
	r, _ := e.requestFor(p)
	return r
}

// requestFor is request, except that a move keeps the booked duration and
// buffer: the offered slots must fit what is stored, not the service's
// current length (a duration edit since the booking would offer a slot the
// conflict check then refuses).
func (e *env) requestFor(p picked) (engine.Request, engine.Line) {
	svc := p.svc
	if p.moveOld != nil && len(p.moveOld.Lines) > 0 {
		var dur int
		for _, l := range p.moveOld.Lines {
			dur += l.DurationMin
		}
		svc.DurationMin = dur
		svc.BufferMin = p.moveOld.BufferMin
	}
	return e.request(svc)
}

func (e *env) formDoc(moveID string, p picked, f bookingForm, lead ...Component) (*Document, error) {
	title := Key("appt.cal.new_booking")
	if moveID != "" {
		title = Key("appt.cal.move")
	}
	doc := &Document{Version: 1, Title: title}
	doc.Components = append(doc.Components, lead...)
	if moveID != "" && p.moveOld != nil {
		doc.Components = append(doc.Components, heading(e.t.lit("appt.cal.moving", e.clientName(*p.moveOld), e.span(*p.moveOld))))
	} else {
		doc.Components = append(doc.Components, heading(title))
	}
	doc.Components = append(doc.Components, e.form(f)...)
	return doc, nil
}

func (e *env) stepOne(a Ask, moveID string, date engine.Date, lead ...Component) (*Document, error) {
	if moveID == "" {
		return e.newBookingWith(bookingForm{submit: "find", date: date, staffID: formStr(a.Form, "staff"),
			sku: formStr(a.Form, "service"), name: formStr(a.Form, "first_name")}, lead...)
	}
	return e.moveStart(moveID, date, lead...)
}

func (e *env) stepOneError(a Ask, moveID string, date engine.Date, err error) (*Document, error) {
	if !isUserError(err) {
		return nil, err
	}
	return e.stepOne(a, moveID, date, e.errNotice(err))
}

func (e *env) dateOrToday(a Ask) engine.Date {
	if d, ok := parseISODate(formStr(a.Form, "date")); ok && !before(d, e.today()) {
		return d
	}
	return e.today()
}

// moveStart draws the first screen of a move: pick another staff member or day.
func (e *env) moveStart(id string, date engine.Date, lead ...Component) (*Document, error) {
	old, ok, err := e.deps.Store.Booking(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return e.dayDocument(date, e.errNotice(errGone))
	}
	if old.Source != "own" {
		return e.dayDocument(e.date(old.Start), e.errNotice(errProviderMove))
	}
	p := picked{moveOld: &old, name: old.FirstName}
	f := bookingForm{submit: "findmove_" + token(id), moveID: id, staffID: old.ResourceID, date: e.date(old.Start)}
	if before(f.date, e.today()) {
		f.date = e.today()
	}
	return e.formDoc(id, p, f, lead...)
}

// place writes a booking (moveID == "") or the replacement row of a move.
func (e *env) place(a Ask, moveID string) (*Document, error) {
	date := e.dateOrToday(a)
	p, err := e.pick(a, moveID)
	if err != nil {
		return e.stepOneError(a, moveID, date, err)
	}
	req, line := e.requestFor(p)
	override := formBool(a.Form, "override")
	actor := e.op.ActorID()
	if override && !e.canOverride() {
		return e.retry(a, moveID, p, e.errNotice(engine.ErrOverrideNotAllowed))
	}
	if override && actor == "" {
		return e.retry(a, moveID, p, e.errNotice(errActorRequired))
	}
	var start time.Time
	if override && formStr(a.Form, "override_time") != "" {
		start, err = e.localTime(p.date, formStr(a.Form, "override_time"))
	} else {
		start, err = time.Parse(time.RFC3339, formStr(a.Form, "time"))
		start = start.UTC()
		if err != nil {
			err = errTimeInvalid
		}
	}
	if err == nil && start.Before(ceilMinute(e.now)) {
		err = errTimeInvalid
	}
	if err != nil {
		return e.retry(a, moveID, p, e.errNotice(err))
	}
	cand := engine.Appointment{
		ID: e.deps.IDs(), OriginTillID: e.shop.OriginTill, Source: "own", ResourceID: p.staff.ID,
		Start: start, End: start.Add(time.Duration(line.DurationMin) * time.Minute),
		BufferMin: line.BufferMin, State: engine.Booked, Lines: []engine.Line{line}, CreatedAt: e.now,
	}
	if p.moveOld != nil {
		if cand, err = engine.Supersede(p.moveOld.Appointment, start, p.staff.ID, cand.ID, e.deps.IDs); err != nil {
			return e.dayDocument(e.date(p.moveOld.Start), e.errNotice(err))
		}
	}
	from, to := e.bounds(e.date(start))
	to = to.Add(24 * time.Hour) // a late service can run past midnight
	around, err := e.deps.Store.Bookings(from.Add(-24*time.Hour), to)
	if err != nil {
		return nil, err
	}
	var ov *engine.Override
	if override {
		ov = &engine.Override{ByManager: e.op.IsManager(), Reason: formStr(a.Form, "override_reason"), ActorUserID: actor}
	}
	res, err := engine.CheckConflict(cand, engineAppts(around), e.policy, ov, clockAt{e.now}, e.deps.IDs)
	if err != nil {
		return e.conflictDoc(a, moveID, p, res, around, err)
	}
	if !override {
		slots, err := e.freeSlots(p.staff.ID, req, p.date, moveID)
		if err != nil {
			return e.retry(a, moveID, p, e.errNotice(err))
		}
		if !hasSlot(slots, start) {
			return e.retry(a, moveID, p, e.errNotice(errSlotTaken))
		}
	}
	ev := engine.Event{ID: e.deps.IDs(), AppointmentID: cand.ID, ActorUserID: actor, At: e.now,
		Kind: engine.KindStateChange, ToState: cand.State}
	ch := Change{Insert: &Booking{Appointment: cand, FirstName: p.name}, Events: append(res.Events, ev)}
	if p.moveOld != nil {
		ch.Superseded = p.moveOld.ID
	}
	if err := e.deps.Store.Commit(ch); err != nil {
		if errors.Is(err, engine.ErrConflict) { // another event took the slot since the check
			return e.retry(a, moveID, p, e.errNotice(errSlotTaken))
		}
		return nil, err
	}
	msg := "appt.cal.booked"
	if p.moveOld != nil {
		msg = "appt.cal.moved"
	}
	return e.dayDocument(e.date(start), notice("info", e.t.lit(msg, e.clientName(Booking{FirstName: p.name}), p.staff.Name, e.hm(start))))
}

func hasSlot(slots []engine.Slot, start time.Time) bool {
	for _, s := range slots {
		if s.Start.Equal(start) {
			return true
		}
	}
	return false
}

// localTime reads "HH:MM" as shop wall-clock time on a date. A time the clock
// skips (the spring-forward hour) is refused; a repeated time (autumn) takes
// its first occurrence.
func (e *env) localTime(d engine.Date, s string) (time.Time, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, errTimeInvalid
	}
	l := time.Date(d.Y, d.M, d.D, t.Hour(), t.Minute(), 0, 0, e.loc)
	if l.Hour() != t.Hour() || l.Minute() != t.Minute() {
		return time.Time{}, errTimeInvalid
	}
	return l.UTC(), nil
}

// retry shows the time step again with a notice, keeping what was chosen.
func (e *env) retry(a Ask, moveID string, p picked, lead ...Component) (*Document, error) {
	slots, err := e.freeSlots(p.staff.ID, mustReq(e, p), p.date, moveID)
	if err != nil {
		return e.stepOneError(a, moveID, p.date, err)
	}
	f := p.formFor(createAction(moveID), moveID)
	f.two, f.slots, f.override = true, slots, e.canOverride()
	return e.formDoc(moveID, p, f, lead...)
}

// conflictDoc answers a refused write: the overlap is named and, for a
// manager where the shop allows double booking, the override fields are shown
// (a reason is mandatory and the actor is recorded).
func (e *env) conflictDoc(a Ask, moveID string, p picked, res engine.WriteResult, around []Booking, err error) (*Document, error) {
	switch {
	case errors.Is(err, engine.ErrConflict), errors.Is(err, engine.ErrOverrideNotAllowed):
		lead := []Component{notice("error", e.t.lit("appt.cal.overlap", e.overlapNames(res.ConflictingIDs, around)))}
		if errors.Is(err, engine.ErrOverrideNotAllowed) {
			lead = append(lead, e.errNotice(err))
		}
		return e.retry(a, moveID, p, lead...)
	case isUserError(err):
		lead := []Component{e.errNotice(err)}
		if len(res.ConflictingIDs) > 0 {
			lead = append([]Component{notice("error", e.t.lit("appt.cal.overlap", e.overlapNames(res.ConflictingIDs, around)))}, lead...)
		}
		return e.retry(a, moveID, p, lead...)
	}
	return nil, err
}

func (e *env) overlapNames(ids []string, around []Booking) string {
	var parts []string
	for _, id := range ids {
		for _, b := range around {
			if b.ID == id {
				parts = append(parts, fmt.Sprintf("%s %s", e.span(b), e.clientName(b)))
			}
		}
	}
	return strings.Join(parts, "; ")
}
