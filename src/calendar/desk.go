package calendar

import (
	"errors"
	"strings"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/checkout"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// Eraser is implemented by a Store that can blank a client's first name on a
// booking and its whole chain at once (a client's erasure request,
// appointment-v1 section 1). A Store without it gets no erase button.
type Eraser interface {
	Erase(appointmentID, actorUserID string) (int, error)
}

// Receipt is a paid till receipt the operator can pick to complete a visit.
type Receipt struct {
	ID       string // sale id
	No       string // printed receipt number
	At       time.Time
	Total    money.Money
	IsRefund bool
}

// ReceiptDesk is implemented by a Store that can list recent receipts and
// complete a visit with one (the manual path of checkout.Processor, B2). A
// Store without it gets no "complete with a receipt" button.
type ReceiptDesk interface {
	// Receipts lists recent paid sales, newest first.
	Receipts() ([]Receipt, error)
	CompleteByReceipt(appointmentID, saleID, actorUserID string) (checkout.Outcome, error)
}

// maxReceipts is the number of receipts offered in the picker.
const maxReceipts = 30

func init() {
	userErrors = append(userErrors, checkout.ErrReceiptNotPaid, checkout.ErrUnknownAppointment, errNoReceipt, ErrReceiptsDenied)
}

var errNoReceipt = errors.New("no_receipt")

// ErrReceiptsDenied is the receipt lookup refused for this role (no view:sales,
// for example a cashier). The picker says so instead of showing a host error
// page (#7267).
var ErrReceiptsDenied = errors.New("receipts_denied")

func (e *env) canErase(b Booking) bool {
	_, ok := e.deps.Store.(Eraser)
	return ok && b.FirstName != "" && e.op.IsManager()
}

func (e *env) canReceipt(b Booking) bool {
	_, ok := e.deps.Store.(ReceiptDesk)
	return ok && b.Source == "own" && (b.State == engine.CheckedIn || b.State == engine.InService)
}

// deskButtons are the extra detail buttons of this file.
func (e *env) deskButtons(b Booking) []Component {
	tk := token(b.ID)
	var out []Component
	if e.canReceipt(b) {
		out = append(out, button("receipt_"+tk, Key("appt.cal.v.receipt"), "primary"))
	}
	if e.canErase(b) {
		out = append(out, button("erasecheck_"+tk, Key("appt.cal.v.erase"), "danger"))
	}
	return out
}

// eraseCheck is the confirmation screen of an erasure.
func (e *env) eraseCheck(b Booking) *Document {
	tk := token(b.ID)
	return &Document{Version: 1, Title: Key("appt.cal.confirm"), Components: []Component{
		heading(e.t.lit("appt.cal.erase_q", e.clientName(b), e.span(b))),
		notice("warn", Key("appt.cal.erase_body")),
		button("erase_"+tk, Key("appt.cal.v.erase"), "danger"),
		button("open_"+tk, Key("appt.cal.back"), "secondary"),
	}}
}

// erase blanks the name on the booking's whole chain.
func (e *env) erase(b Booking) (*Document, error) {
	if !e.op.IsManager() {
		return e.detail(b, e.errNotice(errManagerRequired)), nil
	}
	er, ok := e.deps.Store.(Eraser)
	if !ok {
		return e.detail(b, e.errNotice(engine.ErrInvalid)), nil
	}
	if _, err := er.Erase(b.ID, e.op.ActorID()); err != nil {
		if errors.Is(err, ErrGone) {
			return e.dayDocument(e.date(b.Start), e.errNotice(errGone))
		}
		return nil, err
	}
	fresh, ok, err := e.deps.Store.Booking(b.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return e.dayDocument(e.date(b.Start), notice("info", Key("appt.cal.erased")))
	}
	return e.detail(fresh, notice("info", Key("appt.cal.erased"))), nil
}

// receiptPicker lists recent paid receipts to complete the visit with.
func (e *env) receiptPicker(b Booking) (*Document, error) {
	rd, ok := e.deps.Store.(ReceiptDesk)
	if !ok || !e.canReceipt(b) {
		return e.detail(b, e.errNotice(engine.ErrTransitionNotAllowed)), nil
	}
	rs, err := rd.Receipts()
	if errors.Is(err, ErrReceiptsDenied) {
		return e.detail(b, e.errNotice(err)), nil
	}
	if err != nil {
		return nil, err
	}
	tk := token(b.ID)
	doc := &Document{Version: 1, Title: Key("appt.cal.title")}
	doc.Components = append(doc.Components, heading(Key("appt.cal.receipt_title")), para(Key("appt.cal.receipt_hint")))
	var opts []Component
	for _, r := range rs {
		if r.IsRefund || r.ID == "" {
			continue
		}
		if len(opts) >= maxReceipts {
			break
		}
		opts = append(opts, option(r.ID, Lit(e.hm(r.At)+" #"+r.No+" "+r.Total.String())))
	}
	if len(opts) == 0 {
		doc.Components = append(doc.Components, emptyState(Key("appt.cal.receipt_none"), Key("appt.cal.receipt_none_body")))
	} else {
		doc.Components = append(doc.Components, form("finish_"+tk, Key("appt.cal.v.receipt"),
			field("receipt", Key("appt.cal.receipt_field"), "select", true, nil, opts)))
	}
	doc.Components = append(doc.Components, button("open_"+tk, Key("appt.cal.back"), "secondary"))
	return doc, nil
}

// finish completes the visit with the picked receipt.
func (e *env) finish(a Ask, b Booking) (*Document, error) {
	rd, ok := e.deps.Store.(ReceiptDesk)
	if !ok || !e.canReceipt(b) {
		return e.detail(b, e.errNotice(engine.ErrTransitionNotAllowed)), nil
	}
	sale := strings.TrimSpace(formStr(a.Form, "receipt"))
	if sale == "" {
		return e.detail(b, e.errNotice(errNoReceipt)), nil
	}
	out, err := rd.CompleteByReceipt(b.ID, sale, e.op.ActorID())
	if err != nil {
		if isUserError(err) {
			return e.detail(b, e.errNotice(err)), nil
		}
		if errors.Is(err, ErrGone) {
			return e.dayDocument(e.date(b.Start), e.errNotice(errGone))
		}
		return nil, err
	}
	switch out {
	case checkout.Completed, checkout.Duplicate:
		return e.dayDocument(e.date(b.Start), notice("info", e.t.lit("appt.cal.receipt_done", e.clientName(b))))
	case checkout.OtherSale:
		return e.detail(b, notice("warn", Key("appt.cal.receipt_other"))), nil
	}
	return e.detail(b, e.errNotice(engine.ErrTransitionNotAllowed)), nil
}
