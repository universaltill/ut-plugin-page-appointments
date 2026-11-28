package calendar

import (
	"errors"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// userErrors are refusals the operator can act on; anything else is a store
// failure and goes back to the host as an error.
var userErrors = []error{
	engine.ErrConflict, engine.ErrInvalid, engine.ErrOverrideNotAllowed, engine.ErrReasonRequired,
	engine.ErrTransitionNotAllowed, engine.ErrNoShowTooEarly, engine.ErrSaleRefRequired, engine.ErrNoServices,
	engine.ErrInactiveService, engine.ErrResourceNotEligible, engine.ErrInvalidPolicy, engine.ErrDepositTooLarge,
	engine.ErrDepositInvalid, engine.ErrAmountTooLarge,
	ErrNameRequired, ErrNameTooLong, ErrNameInvalid,
	errActorRequired, errSlotTaken, errDatePast, errTimeInvalid, errProviderMove, errNoStaff, errGone,
	errAckRequired, errManagerRequired,
}

var (
	errAckRequired     = errors.New("ack_required")
	errManagerRequired = errors.New("manager_required")
)

func isUserError(err error) bool {
	for _, u := range userErrors {
		if errors.Is(err, u) {
			return true
		}
	}
	return false
}

// errNotice is an error notice whose text is the locale key of the error's
// wire code.
func (e *env) errNotice(err error) Component {
	if !isUserError(err) {
		return notice("error", Key("appt.cal.err.unknown"))
	}
	return notice("error", Key("appt.cal.err."+err.Error()))
}

// verb is a desk action on one booking.
type verb struct {
	name     string
	to       engine.State
	confirm  bool // goes through a confirm screen first
	manager  bool // only a manager may do it
	label    string
	style    string
	needsWhy func(from engine.State) bool // a reason is mandatory
}

var verbs = map[string]verb{
	"checkin": {name: "checkin", to: engine.CheckedIn, label: "appt.cal.v.checkin", style: "primary"},
	"start":   {name: "start", to: engine.InService, label: "appt.cal.v.start", style: "primary"},
	"unin":    {name: "unin", to: engine.Booked, label: "appt.cal.v.unin", style: "secondary"},
	"noshow":  {name: "noshow", to: engine.NoShow, confirm: true, label: "appt.cal.v.noshow", style: "danger"},
	"unns":    {name: "unns", to: engine.CheckedIn, confirm: true, manager: true, label: "appt.cal.v.unns", style: "primary", needsWhy: func(engine.State) bool { return true }},
	"cancelc": {name: "cancelc", to: engine.CancelledByClient, confirm: true, label: "appt.cal.v.cancelc", style: "danger"},
	"cancels": {name: "cancels", to: engine.CancelledByShop, confirm: true, label: "appt.cal.v.cancels", style: "danger", needsWhy: func(from engine.State) bool { return from == engine.InService }},
}

// verbsFor lists the desk actions that make sense for a booking right now, in
// button order. The engine still decides; this only avoids offering buttons
// that must fail.
func (e *env) verbsFor(b Booking) []verb {
	var names []string
	switch b.State {
	case engine.Booked, engine.Confirmed:
		names = []string{"checkin"}
		if !e.now.Before(b.Start.Add(time.Duration(e.policy.NoShowGraceMin) * time.Minute)) {
			names = append(names, "noshow")
		}
		names = append(names, "cancelc", "cancels")
	case engine.CheckedIn:
		names = []string{"start", "unin", "cancelc", "cancels"}
	case engine.InService:
		names = []string{"cancels"}
	case engine.NoShow:
		names = []string{"unns"}
	}
	var out []verb
	for _, n := range names {
		v := verbs[n]
		if v.manager && !e.op.IsManager() {
			continue
		}
		out = append(out, v)
	}
	return out
}

func (e *env) detail(b Booking, lead ...Component) *Document {
	doc := &Document{Version: 1, Title: Key("appt.cal.title")}
	doc.Components = append(doc.Components, lead...)
	doc.Components = append(doc.Components,
		heading(Lit(e.hm(b.Start)+" "+e.clientName(b))),
		list(
			e.t.lit("appt.cal.d_service", e.serviceNames(b)),
			e.t.lit("appt.cal.d_staff", e.staffName(b.ResourceID)),
			e.t.lit("appt.cal.d_time", e.dateLabel(e.date(b.Start)), e.span(b)),
			e.t.lit("appt.cal.d_state", e.stateLabel(b)),
		))
	if b.Source == "provider" {
		doc.Components = append(doc.Components, notice("info", Key("appt.cal.provider_note")))
	}
	tk := token(b.ID)
	vs := e.verbsFor(b)
	for _, v := range vs {
		act := "do_"
		if v.confirm {
			act = "ask_"
		}
		doc.Components = append(doc.Components, button(act+v.name+"_"+tk, Key(v.label), v.style))
	}
	if b.Source == "own" && (b.State == engine.Booked || b.State == engine.Confirmed) {
		doc.Components = append(doc.Components, button("move_"+tk, Key("appt.cal.move"), "secondary"))
	}
	if b.State == engine.NoShow && !e.op.IsManager() {
		doc.Components = append(doc.Components, para(Key("appt.cal.ask_manager")))
	}
	if b.State == engine.InService {
		doc.Components = append(doc.Components, para(Key("appt.cal.complete_at_till")))
	}
	if (b.State == engine.Booked || b.State == engine.Confirmed) && !e.hasVerb(vs, "noshow") {
		at := b.Start.Add(time.Duration(e.policy.NoShowGraceMin) * time.Minute)
		doc.Components = append(doc.Components, para(e.t.lit("appt.cal.noshow_from", e.hm(at))))
	}
	doc.Components = append(doc.Components, button("day_"+compactDate(e.date(b.Start)), Key("appt.cal.back"), "secondary"))
	return doc
}

func (e *env) hasVerb(vs []verb, n string) bool {
	for _, v := range vs {
		if v.name == n {
			return true
		}
	}
	return false
}

func (e *env) load1(tk string) (Booking, bool, error) {
	return e.deps.Store.Booking(untoken(tk))
}

// confirm is the confirmation screen. A reason field appears where the rules
// demand one; a no-show or cancel on a provider booking also needs the
// operator to tick that the provider may charge the client.
func (e *env) confirm(b Booking, v verb) *Document {
	doc := &Document{Version: 1, Title: Key("appt.cal.confirm")}
	tk := token(b.ID)
	doc.Components = append(doc.Components, heading(e.t.lit("appt.cal.confirm_q", e.t.s(v.label), e.clientName(b), e.span(b))))
	var fields []Component
	if b.Source == "provider" && (v.to == engine.NoShow || v.to == engine.CancelledByShop || v.to == engine.CancelledByClient) {
		doc.Components = append(doc.Components, notice("warn", Key("appt.cal.provider_charge")))
		fields = append(fields, field("ack", Key("appt.cal.provider_ack"), "toggle", true, false, nil))
	}
	if v.needsWhy != nil && v.needsWhy(b.State) {
		doc.Components = append(doc.Components, para(Key("appt.cal.reason_hint")))
		fields = append(fields, field("reason", Key("appt.cal.reason"), "text", true, nil, nil))
	}
	if len(fields) > 0 {
		doc.Components = append(doc.Components, form("do_"+v.name+"_"+tk, Key(v.label), fields...))
	} else {
		doc.Components = append(doc.Components, button("do_"+v.name+"_"+tk, Key(v.label), v.style))
	}
	doc.Components = append(doc.Components, button("open_"+tk, Key("appt.cal.back"), "secondary"))
	return doc
}

// do applies a desk action.
func (e *env) do(a Ask, v verb, b Booking) (*Document, error) {
	day := e.date(b.Start)
	fail := func(err error) (*Document, error) {
		if !isUserError(err) {
			return nil, err
		}
		return e.detail(b, e.errNotice(err)), nil
	}
	if v.manager && !e.op.IsManager() {
		return fail(errManagerRequired)
	}
	if b.Source == "provider" && v.confirm && (v.to == engine.NoShow || v.to == engine.CancelledByShop || v.to == engine.CancelledByClient) && !formBool(a.Form, "ack") {
		return e.confirm(b, v), nil
	}
	in := engine.TransitionInput{To: v.to, ActorUserID: e.op.ActorID(), Now: e.now,
		ByManager: e.op.IsManager(), Reason: strings.TrimSpace(formStr(a.Form, "reason"))}
	if v.needsWhy == nil || !v.needsWhy(b.State) {
		in.Reason = "" // a reason is only kept where the rules ask for one
	}
	if v.needsWhy != nil && v.needsWhy(b.State) && e.op.ActorID() == "" {
		return fail(errActorRequired)
	}
	next, ev, err := engine.Transition(b.Appointment, in, e.policy, e.deps.IDs)
	if err != nil {
		if errors.Is(err, engine.ErrReasonRequired) {
			return e.confirm(b, v).withLead(e.errNotice(err)), nil
		}
		return fail(err)
	}
	events := []engine.Event{ev}
	if b.State == engine.NoShow && v.to == engine.CheckedIn {
		// The chair is held again: the corrected booking must not overlap one
		// that took the slot meanwhile, unless the shop allows double booking
		// and the manager's reason stands as the override reason.
		from, to := e.bounds(day)
		around, err := e.deps.Store.Bookings(from.Add(-24*time.Hour), to.Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		ov := &engine.Override{ByManager: true, Reason: in.Reason, ActorUserID: in.ActorUserID}
		res, err := engine.CheckConflict(next, engineAppts(around), e.policy, ov, clockAt{e.now}, e.deps.IDs)
		if err != nil {
			if errors.Is(err, engine.ErrOverrideNotAllowed) {
				return e.detail(b, notice("error", e.t.lit("appt.cal.overlap", e.overlapNames(res.ConflictingIDs, around)))), nil
			}
			return fail(err)
		}
		events = append(events, res.Events...)
	}
	nb := Booking{Appointment: next, FirstName: b.FirstName}
	if err := e.deps.Store.Commit(Change{Update: &nb, Events: events}); err != nil {
		return nil, err
	}
	msg := notice("info", e.t.lit("appt.cal.done", e.t.s(v.label), e.clientName(b)))
	return e.dayDocument(day, msg)
}

func (d *Document) withLead(c Component) *Document {
	d.Components = append([]Component{c}, d.Components...)
	return d
}
