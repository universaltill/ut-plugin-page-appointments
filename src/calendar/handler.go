package calendar

import (
	"strings"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// Handle answers a ui.view.ask (a.Action == "") or a ui.action.ask. A refusal
// the operator can act on is a document with a notice; only a store failure is
// an error (the host then shows its own "can't be shown" notice).
func (d Deps) Handle(a Ask) (Answer, error) {
	e, err := d.load(a)
	if err != nil {
		return Answer{}, err
	}
	var doc *Document
	if a.Action == "" {
		doc, err = e.view(a)
	} else {
		var redirect string
		doc, redirect, err = e.act(a)
		if redirect != "" {
			return Answer{Redirect: redirect}, nil
		}
	}
	if err != nil {
		return Answer{}, err
	}
	return Answer{Document: doc}, nil
}

// view draws the page a GET asks for.
func (e *env) view(a Ask) (*Document, error) {
	if a.View == ViewToday {
		return e.todayDocument()
	}
	d := e.today()
	if p, ok := parseISODate(a.Params["date"]); ok {
		d = p
	}
	return e.dayDocument(d)
}

// act dispatches a posted action. A redirect is only ever the plugin's own
// calendar route.
func (e *env) act(a Ask) (*Document, string, error) {
	overlay := a.View == ViewToday
	back := func(lead ...Component) (*Document, string, error) {
		if overlay {
			doc, err := e.todayDocument(lead...)
			return doc, "", err
		}
		doc, err := e.dayDocument(e.dateOrToday(a), lead...)
		return doc, "", err
	}
	name := a.Action
	switch {
	case name == "today":
		doc, err := e.dayDocument(e.today())
		return doc, "", err
	case strings.HasPrefix(name, "day_"):
		if d, ok := parseCompactDate(strings.TrimPrefix(name, "day_")); ok {
			doc, err := e.dayDocument(d)
			return doc, "", err
		}
	case name == "opencal":
		return nil, e.deps.Route, nil
	case name == "new":
		doc, err := e.newBooking(e.dateOrToday(a))
		return doc, "", err
	case name == "find":
		doc, err := e.findTimes(a, "")
		return doc, "", err
	case name == "create":
		doc, err := e.place(a, "")
		return doc, "", err
	case name == "queue_add":
		doc, err := e.queueAdd(a)
		return doc, "", err
	case strings.HasPrefix(name, "seat_"):
		doc, err := e.queueSeat(strings.TrimPrefix(name, "seat_"))
		return doc, "", err
	case strings.HasPrefix(name, "drop_"):
		doc, err := e.queueDrop(strings.TrimPrefix(name, "drop_"))
		return doc, "", err
	case strings.HasPrefix(name, "open_"):
		return e.onBooking(strings.TrimPrefix(name, "open_"), back, func(b Booking) (*Document, error) { return e.detail(b), nil })
	case strings.HasPrefix(name, "ci_"):
		doc, err := e.quickCheckIn(strings.TrimPrefix(name, "ci_"))
		return doc, "", err
	case strings.HasPrefix(name, "move_"):
		tk := strings.TrimPrefix(name, "move_")
		doc, err := e.moveStart(untoken(tk), e.dateOrToday(a))
		return doc, "", err
	case strings.HasPrefix(name, "findmove_"):
		doc, err := e.findTimes(a, untoken(strings.TrimPrefix(name, "findmove_")))
		return doc, "", err
	case strings.HasPrefix(name, "domove_"):
		doc, err := e.place(a, untoken(strings.TrimPrefix(name, "domove_")))
		return doc, "", err
	case strings.HasPrefix(name, "erasecheck_"):
		return e.onBooking(strings.TrimPrefix(name, "erasecheck_"), back, func(b Booking) (*Document, error) { return e.eraseCheck(b), nil })
	case strings.HasPrefix(name, "erase_"):
		return e.onBooking(strings.TrimPrefix(name, "erase_"), back, e.erase)
	case strings.HasPrefix(name, "receipt_"):
		return e.onBooking(strings.TrimPrefix(name, "receipt_"), back, e.receiptPicker)
	case strings.HasPrefix(name, "finish_"):
		return e.onBooking(strings.TrimPrefix(name, "finish_"), back, func(b Booking) (*Document, error) { return e.finish(a, b) })
	case strings.HasPrefix(name, "ask_"), strings.HasPrefix(name, "do_"):
		ask := strings.HasPrefix(name, "ask_")
		parts := strings.SplitN(name, "_", 3)
		v, ok := verbs[parts[1]]
		if len(parts) == 3 && ok {
			return e.onBooking(parts[2], back, func(b Booking) (*Document, error) {
				if ask {
					return e.confirm(b, v), nil
				}
				return e.do(a, v, b)
			})
		}
	}
	return back(e.errNotice(engine.ErrInvalid))
}

// onBooking loads the booking an action names and runs fn on it. A booking
// that has gone (superseded, purged) is a notice, never a crash.
func (e *env) onBooking(tk string, back func(...Component) (*Document, string, error),
	fn func(Booking) (*Document, error)) (*Document, string, error) {
	b, ok, err := e.load1(tk)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return back(e.errNotice(errGone))
	}
	doc, err := fn(b)
	if err != nil {
		return nil, "", err
	}
	return doc, "", nil
}
