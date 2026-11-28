package calendar

import (
	"errors"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// anyStaff is the select value for "first free chair".
const anyStaff = "any"

func ceilMinute(t time.Time) time.Time {
	r := t.Truncate(time.Minute)
	if r.Before(t) {
		r = r.Add(time.Minute)
	}
	return r
}

func (e *env) resources() []engine.Resource {
	out := make([]engine.Resource, 0, len(e.staff))
	for _, s := range e.staff {
		out = append(out, s.Resource)
	}
	return out
}

// orderedQueue returns the queue in arrival order with the engine walk-ins
// split out, so estimates and the table agree.
func (e *env) orderedQueue() ([]Queued, error) {
	q, err := e.deps.Store.Queue()
	if err != nil {
		return nil, err
	}
	byID := map[string]Queued{}
	ws := make([]engine.Walkin, len(q))
	for i, x := range q {
		byID[x.ID] = x
		ws[i] = x.Walkin
	}
	out := make([]Queued, 0, len(q))
	for _, w := range engine.OrderQueue(ws) {
		out = append(out, byID[w.ID])
	}
	return out, nil
}

// estimates returns, per queued walk-in in order, its nearest free start
// given everyone ahead of it is placed first.
func (e *env) estimates(q []Queued) ([]engine.Estimate, error) {
	if len(q) == 0 {
		return nil, nil
	}
	from, _ := e.bounds(e.today())
	to, _ := e.bounds(addDays(e.today(), 9))
	bs, err := e.deps.Store.Bookings(from, to)
	if err != nil {
		return nil, err
	}
	existing := engineAppts(bs)
	res := e.resources()
	out := make([]engine.Estimate, len(q))
	var ahead []engine.Walkin
	for i, w := range q {
		out[i] = engine.EstimateNearestFree(e.loc, res, existing, ahead, w.Walkin, e.policy, e.now)
		ahead = append(ahead, w.Walkin)
	}
	return out, nil
}

func (e *env) queueName(w Queued) string {
	if w.FirstName == "" {
		return e.t.s("appt.cal.walkin_client")
	}
	return w.FirstName
}

func (e *env) estimateText(est engine.Estimate) string {
	if !est.OK {
		return e.t.s("appt.cal.q_no_chair")
	}
	return e.hm(est.Start) + " " + e.staffName(est.ResourceID)
}

// maxQueueButtons caps the seat and drop buttons (two per entry) so a long
// queue on a busy day stays under the view format's component limit; the
// rest of the queue is still listed in the table, in order (N15).
const maxQueueButtons = 20

// queuePanel is the walk-in queue: who waits, the estimated start of each,
// the buttons to seat or remove one, and the form to add one.
func (e *env) queuePanel() ([]Component, error) {
	q, err := e.orderedQueue()
	if err != nil {
		return nil, err
	}
	ests, err := e.estimates(q)
	if err != nil {
		return nil, err
	}
	out := []Component{heading(Key("appt.cal.queue"))}
	if len(q) == 0 {
		out = append(out, emptyState(Key("appt.cal.q_empty"), Key("appt.cal.q_empty_body")))
	} else {
		rows := make([][]any, len(q))
		for i, w := range q {
			rows[i] = []any{float64(i + 1), e.queueName(w), e.serviceLabel(w.ServiceSKU), e.estimateText(ests[i])}
		}
		out = append(out, table(
			[]Component{Key("appt.cal.q_pos"), Key("appt.cal.col_client"), Key("appt.cal.col_service"), Key("appt.cal.q_estimate")},
			[]string{"number", "text", "text", "text"}, rows))
		for i, w := range q {
			tk := token(w.ID)
			if tk == "" {
				continue
			}
			if i >= maxQueueButtons {
				out = append(out, notice("warn", e.t.lit("appt.cal.q_too_many", maxQueueButtons)))
				break
			}
			out = append(out,
				button("seat_"+tk, e.t.lit("appt.cal.q_seat", e.queueName(w)), "primary"),
				button("drop_"+tk, e.t.lit("appt.cal.q_drop", e.queueName(w)), "danger"))
		}
	}
	if f := e.queueForm(); f != nil {
		out = append(out, f)
	}
	return out, nil
}

func (e *env) serviceLabel(sku string) string {
	if s, ok := e.serviceBySKU(sku); ok {
		return s.Name
	}
	return sku
}

func (e *env) activeServiceOptions() []Component {
	var out []Component
	for _, s := range e.services {
		if s.Active {
			out = append(out, option(s.SKU, Lit(s.Name)))
		}
	}
	return out
}

func (e *env) queueForm() Component {
	svcs := e.activeServiceOptions()
	if len(svcs) == 0 {
		return nil
	}
	chairs := []Component{option(anyStaff, Key("appt.cal.q_any_chair"))}
	for _, s := range e.staff {
		if s.Active {
			chairs = append(chairs, option(s.ID, Lit(s.Name)))
		}
	}
	return form("queue_add", Key("appt.cal.q_add"),
		field("first_name", Key("appt.cal.first_name_only"), "text", false, nil, nil),
		field("service", Key("appt.cal.service"), "select", true, nil, svcs),
		field("staff", Key("appt.cal.staff"), "select", true, anyStaff, chairs))
}

func (e *env) nextSeq(q []Queued) int64 {
	var m int64
	for _, w := range q {
		if w.Seq > m {
			m = w.Seq
		}
	}
	return m + 1
}

// queueAdd adds a walk-in to the queue.
func (e *env) queueAdd(a Ask) (*Document, error) {
	name, err := CleanName(formStr(a.Form, "first_name"), true)
	if err != nil {
		return e.dayDocument(e.today(), e.errNotice(err))
	}
	svc, ok := e.serviceBySKU(formStr(a.Form, "service"))
	if !ok || !svc.Active {
		return e.dayDocument(e.today(), e.errNotice(engine.ErrInactiveService))
	}
	var ids []string
	if sid := formStr(a.Form, "staff"); sid != "" && sid != anyStaff {
		if s, ok := e.staffByID(sid); !ok || !s.Active {
			return e.dayDocument(e.today(), e.errNotice(engine.ErrInvalid))
		}
		ids = []string{sid}
	}
	q, err := e.deps.Store.Queue()
	if err != nil {
		return nil, err
	}
	w := Queued{FirstName: name, ServiceSKU: svc.SKU, Walkin: engine.Walkin{
		ID: e.deps.IDs(), Seq: e.nextSeq(q), Arrived: e.now,
		DurationMin: svc.DurationMin, BufferMin: svc.BufferMin, ResourceIDs: ids,
	}}
	if err := e.deps.Store.Commit(Change{Enqueue: &w}); err != nil {
		return nil, err
	}
	return e.dayDocument(e.today(), notice("info", e.t.lit("appt.cal.q_added", e.queueName(w))))
}

// queueDrop removes a walk-in who left.
func (e *env) queueDrop(tk string) (*Document, error) {
	q, err := e.deps.Store.Queue()
	if err != nil {
		return nil, err
	}
	for _, w := range q {
		if token(w.ID) == tk {
			if err := e.deps.Store.Commit(Change{Dequeue: w.ID}); err != nil {
				return nil, err
			}
			return e.dayDocument(e.today(), notice("info", e.t.lit("appt.cal.q_dropped", e.queueName(w))))
		}
	}
	return e.dayDocument(e.today(), e.errNotice(errGone))
}

// queueSeat turns a queued walk-in into a checked-in own walk-in on the chair
// the estimate named, but only when that chair is free right now: seating
// never double-books and never needs an override.
func (e *env) queueSeat(tk string) (*Document, error) {
	q, err := e.orderedQueue()
	if err != nil {
		return nil, err
	}
	ests, err := e.estimates(q)
	if err != nil {
		return nil, err
	}
	for i, w := range q {
		if token(w.ID) != tk {
			continue
		}
		est := ests[i]
		if !est.OK {
			return e.dayDocument(e.today(), notice("warn", Key("appt.cal.q_no_chair")))
		}
		if est.Start.After(ceilMinute(e.now)) {
			return e.dayDocument(e.today(), notice("warn", e.t.lit("appt.cal.q_not_yet", e.hm(est.Start), e.staffName(est.ResourceID))))
		}
		svc, _ := e.serviceBySKU(w.ServiceSKU)
		draft := engine.Appointment{
			// The seated row reuses the queue id: the queue never lists an id that is
			// an appointment, so a crash before Dequeue cannot seat a walk-in twice.
			ID: w.ID, OriginTillID: e.shop.OriginTill, ResourceID: est.ResourceID,
			Start: est.Start, End: est.Start.Add(time.Duration(w.DurationMin) * time.Minute),
			BufferMin: w.BufferMin,
			Lines: []engine.Line{{SKU: w.ServiceSKU, Name: svc.Name, Qty: 1, Amount: svc.Price,
				DurationMin: w.DurationMin, BufferMin: w.BufferMin}},
		}
		walk, ev, err := engine.NewWalkIn(draft, e.op.ActorID(), e.now, e.deps.IDs)
		if err != nil {
			return e.dayDocument(e.today(), e.errNotice(err))
		}
		from, to := est.Start, walk.End.Add(time.Duration(w.BufferMin)*time.Minute)
		others, err := e.deps.Store.Bookings(from, to)
		if err != nil {
			return nil, err
		}
		if _, err := engine.CheckConflict(walk, engineAppts(others), e.policy, nil, clockAt{e.now}, e.deps.IDs); err != nil {
			return e.dayDocument(e.today(), e.errNotice(err))
		}
		b := Booking{Appointment: walk, FirstName: w.FirstName}
		if err := e.deps.Store.Commit(Change{Insert: &b, Events: []engine.Event{ev}, Dequeue: w.ID}); err != nil {
			return nil, err
		}
		return e.dayDocument(e.date(est.Start), notice("info", e.t.lit("appt.cal.q_seated", e.queueName(w), e.staffName(est.ResourceID))))
	}
	return e.dayDocument(e.today(), e.errNotice(errGone))
}

type clockAt struct{ t time.Time }

func (c clockAt) Now() time.Time { return c.t }

var errGone = errors.New("gone")

// ErrGone is what a Store returns when the row an action names is superseded,
// missing or no longer in the state the operator saw (a stale or repeated
// post): the operator sees "no longer available", never a host error.
var ErrGone = errGone

func formStr(f map[string]any, k string) string {
	s, _ := f[k].(string)
	return strings.TrimSpace(s)
}

func formBool(f map[string]any, k string) bool {
	b, _ := f[k].(bool)
	return b
}
