// Package logbook is the subscriber-side logic of slice #7116: it parses the
// appointment.completed event into a logbook entry (ADR-0149 linked mode:
// total and lines come from the paid sale) and applies it idempotently.
//
// No logbook plugin repository exists yet (#3700, #3702), so this package
// defines the entry shape it needs behind two small interfaces (EntryStore,
// StaffDirectory) and shares no table with the appointments plugin. It
// belongs to the logbook plugin once that repo exists; see checkout/README.md.
package logbook

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// KeyUnmapped is the en.json key of the notice shown when no entry is
// written because the resource has no logbook staff mapping.
const KeyUnmapped = "appointments.logbook.unmapped"

// ErrBadEvent is returned for a payload that cannot be a valid event.
var ErrBadEvent = errors.New("bad_event")

// Line is one entry line (name and amount are snapshots).
type Line struct {
	SKU         string
	Name        string
	Qty         int
	AmountMinor int64
}

// Entry is the logbook entry derived from one completed appointment. The
// appointment id is the idempotency key. It holds no client data.
type Entry struct {
	ID            string
	AppointmentID string
	StaffRef      string
	Currency      string
	TotalMinor    int64
	SaleRef       string
	Lines         []Line
}

// Event is the parsed appointment.completed payload.
type Event struct {
	AppointmentID string
	ResourceID    string
	StaffRef      string
	Currency      string
	SaleRef       string
	Lines         []Line
}

type wireLine struct {
	SKU         string `json:"sku"`
	Name        string `json:"name_snapshot"`
	Qty         int    `json:"qty"`
	AmountMinor int64  `json:"amount_minor"`
}

type wire struct {
	AppointmentID string     `json:"appointment_id"`
	ResourceID    string     `json:"resource_id"`
	StaffRef      string     `json:"logbook_staff_ref"`
	Currency      string     `json:"currency"`
	Lines         []wireLine `json:"lines"`
	SaleRef       string     `json:"sale_ref"`
}

// Parse validates and decodes a payload strictly: unknown keys (a leaked
// client name, say) are refused, not ignored.
func Parse(b []byte) (Event, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var w wire
	if err := dec.Decode(&w); err != nil {
		return Event{}, fmt.Errorf("%w: %v", ErrBadEvent, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Event{}, fmt.Errorf("%w: trailing data", ErrBadEvent)
	}
	if w.AppointmentID == "" || w.ResourceID == "" || w.Currency == "" || w.SaleRef == "" || len(w.Lines) == 0 {
		return Event{}, fmt.Errorf("%w: missing field", ErrBadEvent)
	}
	e := Event{AppointmentID: w.AppointmentID, ResourceID: w.ResourceID, StaffRef: w.StaffRef, Currency: w.Currency, SaleRef: w.SaleRef}
	var total int64
	for _, l := range w.Lines {
		if l.SKU == "" || l.Qty <= 0 || l.AmountMinor < 0 || total > math.MaxInt64-l.AmountMinor {
			return Event{}, fmt.Errorf("%w: bad line", ErrBadEvent)
		}
		total += l.AmountMinor
		e.Lines = append(e.Lines, Line{SKU: l.SKU, Name: l.Name, Qty: l.Qty, AmountMinor: l.AmountMinor})
	}
	return e, nil
}

// EntryID is a deterministic UUID-shaped id derived from the appointment id,
// so a redelivery maps to the same entry.
func EntryID(appointmentID string) string {
	h := sha256.Sum256([]byte("appointment.completed:" + appointmentID))
	h[6] = h[6]&0x0f | 0x50 // version 5 layout
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// EntryStore inserts an entry keyed by AppointmentID and reports whether it
// was new. It must be atomic (a unique index in the real database).
type EntryStore interface {
	InsertIfAbsent(e Entry) (inserted bool, err error)
}

// StaffDirectory says whether the logbook has a staff member with this ref.
type StaffDirectory interface {
	Has(staffRef string) bool
}

// Outcome of handling one event.
type Outcome string

// Outcomes.
const (
	Written   Outcome = "written"
	Duplicate Outcome = "duplicate"
	Unmapped  Outcome = "unmapped"
)

// Result is what Handle reports. MessageKey is set for Unmapped so the UI
// can say why nothing was written.
type Result struct {
	Outcome    Outcome
	Entry      Entry
	MessageKey string
}

// Consumer applies appointment.completed events.
type Consumer struct {
	Store EntryStore
	Staff StaffDirectory
}

// Handle parses one payload and writes at most one entry. An unmapped
// resource (no ref, or a ref the logbook does not know) writes none.
func (c *Consumer) Handle(payload []byte) (Result, error) {
	ev, err := Parse(payload)
	if err != nil {
		return Result{}, err
	}
	if ev.StaffRef == "" || !c.Staff.Has(ev.StaffRef) {
		return Result{Outcome: Unmapped, MessageKey: KeyUnmapped}, nil
	}
	e := Entry{ID: EntryID(ev.AppointmentID), AppointmentID: ev.AppointmentID, StaffRef: ev.StaffRef,
		Currency: ev.Currency, SaleRef: ev.SaleRef, Lines: ev.Lines}
	for _, l := range ev.Lines {
		e.TotalMinor += l.AmountMinor
	}
	ins, err := c.Store.InsertIfAbsent(e)
	if err != nil {
		return Result{}, err
	}
	if !ins {
		return Result{Outcome: Duplicate, Entry: e}, nil
	}
	return Result{Outcome: Written, Entry: e}, nil
}
