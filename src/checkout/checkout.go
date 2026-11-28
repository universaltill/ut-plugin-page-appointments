// Package checkout holds the pure, host-independent logic of the checkout
// handoff (slice #7115): the basket line builder, idempotent completion on
// sale.completed, the completion event payload and its redelivery-safe
// publisher state, and the manual "complete by picking the receipt" path.
//
// Nothing here talks to the till. Every host contact is a small interface
// (Store, Publisher, ReceiptChecker, Clock). README.md lists which core seam
// each function waits for. Binding rules (appointment-v1.md section 6): no
// deposit logic, and no client name on the basket line or the receipt.
package checkout

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
)

// EventName is the only name the plugin may publish: <plugin id>.<name>.
// A test pins it to manifest.json.
const EventName = "com.universaltill.page-appointments.appointment.completed"

// Errors.
var (
	ErrNotCheckoutState   = errors.New("not_checkout_state")
	ErrNotOwn             = errors.New("not_own_appointment")
	ErrNoLines            = errors.New("no_lines")
	ErrBadLine            = errors.New("bad_line")
	ErrReceiptNotPaid     = errors.New("receipt_not_paid")
	ErrUnknownAppointment = errors.New("unknown_appointment")
)

const refPrefix = "appt:"

// BasketLine is the spec of one add_to_basket proposal. It carries no price:
// the sale price is the catalogue price at checkout. QuoteMinor is the
// booking quote, display only, never forced onto the sale. There is no name,
// no client data and no deposit.
type BasketLine struct {
	SKU        string `json:"sku"`
	Qty        int    `json:"qty"`
	Ref        string `json:"ref"`
	QuoteMinor int64  `json:"quote_minor"`
}

// BuildLines turns an own appointment's service lines into basket line specs,
// one per line, each with the opaque ref "appt:<id>:<index>".
func BuildLines(a engine.Appointment) ([]BasketLine, error) {
	if a.Source != "own" && a.Source != "" {
		return nil, ErrNotOwn
	}
	if a.State != engine.CheckedIn && a.State != engine.InService {
		return nil, ErrNotCheckoutState
	}
	if len(a.Lines) == 0 {
		return nil, ErrNoLines
	}
	if a.ID == "" || strings.Contains(a.ID, ":") {
		return nil, ErrBadLine
	}
	out := make([]BasketLine, 0, len(a.Lines))
	for i, l := range a.Lines {
		if l.SKU == "" || l.Qty <= 0 {
			return nil, ErrBadLine
		}
		out = append(out, BasketLine{SKU: l.SKU, Qty: l.Qty, Ref: refPrefix + a.ID + ":" + strconv.Itoa(i), QuoteMinor: l.Amount.Minor()})
	}
	return out, nil
}

// ParseRef splits a line ref made by BuildLines.
func ParseRef(ref string) (appointmentID string, line int, ok bool) {
	rest, found := strings.CutPrefix(ref, refPrefix)
	if !found {
		return "", 0, false
	}
	i := strings.LastIndex(rest, ":")
	if i <= 0 {
		return "", 0, false
	}
	id := rest[:i]
	if strings.Contains(id, ":") {
		return "", 0, false
	}
	n, err := strconv.Atoi(rest[i+1:])
	if err != nil || n < 0 || strconv.Itoa(n) != rest[i+1:] {
		return "", 0, false
	}
	return id, n, true
}

// SaleLine and SaleCompleted mirror the parts of the core sale.completed
// payload this plugin reads. Ref is the per-line echo that core does not
// provide yet (see README).
type SaleLine struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
	Ref string `json:"ref,omitempty"`
}

// SaleCompleted is the subscribed event payload.
type SaleCompleted struct {
	SaleID    string     `json:"sale_id"`
	LineItems []SaleLine `json:"line_items"`
}

// Completion is the outbox record of one completed appointment. Payload is
// frozen at completion so a republish is byte-identical.
type Completion struct {
	AppointmentID string
	SaleRef       string
	Payload       []byte
	Published     bool
}

// Store is the persistence the host provides. Commit must write the
// appointment state, its events and the outbox record in one transaction.
type Store interface {
	Appointment(id string) (engine.Appointment, bool, error)
	// Currency is the ISO code stored on the appointment record (appointment-v1:
	// stored so that a later currency change never mixes amounts). The completed
	// event is published under it, never under a processor-wide setting.
	Currency(id string) (string, error)
	Commit(a engine.Appointment, evs []engine.Event, c Completion) error
	Pending() ([]Completion, error)
	MarkPublished(appointmentID string) error
}

// Publisher wraps the event_publish host function; Publish returns its code
// (0 = accepted, anything else, for example -5, = not published).
type Publisher interface {
	Publish(name string, payload []byte) int
}

// ReceiptChecker answers whether saleID is a real paid sale
// (core view sales.receipts.v1).
type ReceiptChecker interface {
	Paid(saleID string) (bool, error)
}

// Outcome is what happened to one appointment.
type Outcome string

// Outcomes.
const (
	Completed      Outcome = "completed"
	Duplicate      Outcome = "duplicate"       // already completed by this sale: no-op
	OtherSale      Outcome = "other_sale"      // already completed by a different sale: left alone
	Incomplete     Outcome = "incomplete"      // not every line was in the sale: staff complete by receipt
	NotCompletable Outcome = "not_completable" // state does not allow completion
	Unknown        Outcome = "unknown"         // ref names no appointment
)

// Result is one appointment's outcome.
type Result struct {
	AppointmentID string
	Outcome       Outcome
}

// Processor holds the injected collaborators. All methods are safe for
// concurrent use: they are serialised, so redelivery races collapse to one
// completion.
type Processor struct {
	Store    Store
	Pub      Publisher
	Receipts ReceiptChecker
	Clock    engine.Clock
	IDs      engine.IDGen
	Policy   engine.Policy
	// StaffRef maps a resource id to its logbook_staff_ref ("" = unmapped).
	StaffRef func(resourceID string) string

	mu sync.Mutex
}

// HandleSaleCompleted completes every appointment whose lines all appear in
// the sale by ref. Redelivery of the same sale is a no-op. Unpublished
// completions are flushed at the end, whatever the outcome.
func (p *Processor) HandleSaleCompleted(s SaleCompleted, actor string) ([]Result, error) {
	saleID := strings.TrimSpace(s.SaleID)
	if saleID == "" {
		return nil, engine.ErrSaleRefRequired
	}
	seen := map[string]map[int]bool{}
	var ids []string
	for _, l := range s.LineItems {
		id, n, ok := ParseRef(l.Ref)
		if !ok {
			continue
		}
		if seen[id] == nil {
			seen[id] = map[int]bool{}
			ids = append(ids, id)
		}
		seen[id][n] = true
	}
	sort.Strings(ids)

	p.mu.Lock()
	defer p.mu.Unlock()
	var out []Result
	for _, id := range ids {
		r, err := p.complete(id, saleID, actor, seen[id])
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	if err := p.flush(); err != nil {
		return out, err
	}
	return out, nil
}

// CompleteByReceipt is the manual path (B2): staff pick a receipt for an
// appointment whose sale.completed was missed or never carried a ref. The
// receipt must be a real paid sale.
func (p *Processor) CompleteByReceipt(appointmentID, saleID, actor string) (Result, error) {
	saleID = strings.TrimSpace(saleID)
	if saleID == "" {
		return Result{}, engine.ErrSaleRefRequired
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok, err := p.Store.Appointment(appointmentID)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, ErrUnknownAppointment
	}
	if r, done := settled(a, saleID); done {
		return r, p.flush()
	}
	if a.State != engine.CheckedIn && a.State != engine.InService {
		return Result{AppointmentID: a.ID, Outcome: NotCompletable}, nil
	}
	paid, err := p.Receipts.Paid(saleID)
	if err != nil {
		return Result{}, err
	}
	if !paid {
		return Result{}, ErrReceiptNotPaid
	}
	r, err := p.finish(a, saleID, actor)
	if err != nil {
		return r, err
	}
	return r, p.flush()
}

// Flush republishes every completion not yet accepted by event_publish.
// Call it on the plugin's next event of any kind.
func (p *Processor) Flush() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.flush()
}

func (p *Processor) flush() error {
	pend, err := p.Store.Pending()
	if err != nil {
		return err
	}
	for _, c := range pend {
		if p.Pub.Publish(EventName, c.Payload) != 0 {
			continue // stays pending; retried on the next event
		}
		if err := p.Store.MarkPublished(c.AppointmentID); err != nil {
			return err
		}
	}
	return nil
}

func settled(a engine.Appointment, saleID string) (Result, bool) {
	if a.State != engine.Completed {
		return Result{}, false
	}
	if a.SaleRef == saleID {
		return Result{AppointmentID: a.ID, Outcome: Duplicate}, true
	}
	return Result{AppointmentID: a.ID, Outcome: OtherSale}, true
}

func (p *Processor) complete(id, saleID, actor string, lines map[int]bool) (Result, error) {
	a, ok, err := p.Store.Appointment(id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{AppointmentID: id, Outcome: Unknown}, nil
	}
	if r, done := settled(a, saleID); done {
		return r, nil
	}
	for i := range a.Lines {
		if !lines[i] {
			return Result{AppointmentID: id, Outcome: Incomplete}, nil
		}
	}
	if a.State != engine.CheckedIn && a.State != engine.InService {
		return Result{AppointmentID: id, Outcome: NotCompletable}, nil
	}
	return p.finish(a, saleID, actor)
}

// finish runs checked_in -> in_service (if needed) -> completed through the
// engine, freezes the payload and commits everything in one Store.Commit.
func (p *Processor) finish(a engine.Appointment, saleID, actor string) (Result, error) {
	now := p.Clock.Now()
	var evs []engine.Event
	cur := a
	if cur.State == engine.CheckedIn {
		n, ev, err := engine.Transition(cur, engine.TransitionInput{To: engine.InService, ActorUserID: actor, Now: now}, p.Policy, p.IDs)
		if err != nil {
			return Result{}, err
		}
		cur, evs = n, append(evs, ev)
	}
	n, ev, err := engine.Transition(cur, engine.TransitionInput{To: engine.Completed, ActorUserID: actor, SaleRef: saleID, Now: now}, p.Policy, p.IDs)
	if err != nil {
		return Result{}, err
	}
	evs = append(evs, ev)
	ref := ""
	if p.StaffRef != nil {
		ref = p.StaffRef(n.ResourceID)
	}
	currency, err := p.Store.Currency(n.ID)
	if err != nil {
		return Result{}, err
	}
	payload, err := BuildPayload(n, currency, ref)
	if err != nil {
		return Result{}, err
	}
	if err := p.Store.Commit(n, evs, Completion{AppointmentID: n.ID, SaleRef: saleID, Payload: payload}); err != nil {
		return Result{}, err
	}
	return Result{AppointmentID: n.ID, Outcome: Completed}, nil
}

type payloadLine struct {
	SKU         string `json:"sku"`
	Name        string `json:"name_snapshot"`
	Qty         int    `json:"qty"`
	AmountMinor int64  `json:"amount_minor"`
}

type payload struct {
	AppointmentID   string        `json:"appointment_id"`
	ResourceID      string        `json:"resource_id"`
	LogbookStaffRef string        `json:"logbook_staff_ref,omitempty"`
	Currency        string        `json:"currency"`
	Lines           []payloadLine `json:"lines"`
	SaleRef         string        `json:"sale_ref"`
}

// BuildPayload builds the appointment.completed payload
// (appointment-v1.md section 5). sale_ref is sale.completed.sale_id. The
// payload carries no client name, deposit or note.
func BuildPayload(a engine.Appointment, currency, logbookStaffRef string) ([]byte, error) {
	if strings.TrimSpace(a.SaleRef) == "" {
		return nil, engine.ErrSaleRefRequired
	}
	if currency == "" || a.ID == "" || a.ResourceID == "" {
		return nil, ErrBadLine
	}
	p := payload{AppointmentID: a.ID, ResourceID: a.ResourceID, LogbookStaffRef: logbookStaffRef,
		Currency: currency, Lines: make([]payloadLine, 0, len(a.Lines)), SaleRef: a.SaleRef}
	for _, l := range a.Lines {
		p.Lines = append(p.Lines, payloadLine{SKU: l.SKU, Name: l.Name, Qty: l.Qty, AmountMinor: l.Amount.Minor()})
	}
	return json.Marshal(p)
}
