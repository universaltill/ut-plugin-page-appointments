package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/checkout"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/store"
)

// Checkout is checkout.Store on the repository. The host has no transaction,
// so the three writes of one completion are ordered, not atomic:
//
//  1. the state projection moves to completed (the commit point, guarded on
//     the state the processor decided on),
//  2. the events are appended,
//  3. the frozen payload goes to the outbox.
//
// A crash after 1 leaves a completed appointment without an outbox row;
// Pending finds such rows and rebuilds their payload from the record, so the
// completed event is never lost (and never published twice by one outbox row).
type Checkout struct {
	Ctx      context.Context
	Repo     *store.Repository
	StaffRef func(resourceID string) string
}

var _ checkout.Store = (*Checkout)(nil)

func (c *Checkout) ctx() context.Context {
	if c.Ctx == nil {
		return context.Background()
	}
	return c.Ctx
}

// Appointment returns the current row; a superseded row is not found, so a
// line ref that names an old row of a moved booking completes nothing.
func (c *Checkout) Appointment(id string) (engine.Appointment, bool, error) {
	b, err := c.Repo.Get(c.ctx(), id)
	if errors.Is(err, store.ErrNotFound) {
		return engine.Appointment{}, false, nil
	}
	if err != nil {
		return engine.Appointment{}, false, err
	}
	cur, err := c.Repo.IsCurrent(c.ctx(), id)
	if err != nil || !cur {
		return engine.Appointment{}, false, err
	}
	return b.Appointment, true, nil
}

// Currency is the code stored on the record.
func (c *Checkout) Currency(id string) (string, error) {
	b, err := c.Repo.Get(c.ctx(), id)
	return b.Currency, err
}

// Commit applies one completion in the order described on the type.
// The state write is guarded on from, the state the processor decided on: a
// row another event completed (or moved) meanwhile is refused with
// checkout.ErrConflict, so its sale_ref and its outbox row stay the first
// completion's.
func (c *Checkout) Commit(from engine.State, a engine.Appointment, evs []engine.Event, comp checkout.Completion) error {
	if err := c.Repo.SetState(c.ctx(), a.ID, from, a, evs); err != nil {
		if errors.Is(err, store.ErrStale) || errors.Is(err, store.ErrNotCurrent) {
			return fmt.Errorf("%w: %w", checkout.ErrConflict, calendar.ErrGone)
		}
		return gone(err)
	}
	return c.Repo.InsertOutbox(c.ctx(), a.ID, comp.SaleRef, comp.Payload)
}

// Pending returns the payloads not yet accepted by event_publish, after
// rebuilding the outbox rows a crash left out.
func (c *Checkout) Pending() ([]checkout.Completion, error) {
	ids, err := c.Repo.UnoutboxedCompleted(c.ctx())
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		b, err := c.Repo.Get(c.ctx(), id)
		if err != nil {
			return nil, err
		}
		ref := ""
		if c.StaffRef != nil {
			ref = c.StaffRef(b.ResourceID)
		}
		payload, err := checkout.BuildPayload(b.Appointment, b.Currency, ref)
		if err != nil {
			return nil, err
		}
		if err := c.Repo.InsertOutbox(c.ctx(), id, b.SaleRef, payload); err != nil {
			return nil, err
		}
	}
	rows, err := c.Repo.PendingOutbox(c.ctx())
	if err != nil {
		return nil, err
	}
	out := make([]checkout.Completion, len(rows))
	for i, r := range rows {
		out[i] = checkout.Completion{AppointmentID: r.AppointmentID, SaleRef: r.SaleRef, Payload: r.Payload}
	}
	return out, nil
}

// MarkPublished records that event_publish accepted the payload.
func (c *Checkout) MarkPublished(appointmentID string) error {
	return c.Repo.MarkPublished(c.ctx(), appointmentID)
}

// Receipts reads the till's receipts (the host view sales.receipts.v1).
type Receipts interface {
	// Recent lists today's paid sales, newest first.
	Recent() ([]calendar.Receipt, error)
	checkout.ReceiptChecker
}

// Desk is the calendar's store with the two desk abilities the plain Store
// does not have: erasing a client's name and completing a visit with a
// receipt. calendar offers a button only when the Store it holds has them.
type Desk struct {
	*Store
	Proc   *checkout.Processor
	Source Receipts
}

var (
	_ calendar.Eraser      = (*Desk)(nil)
	_ calendar.ReceiptDesk = (*Desk)(nil)
)

// Erase blanks the first name on the booking's whole chain.
func (d *Desk) Erase(id, actor string) (int, error) {
	n, err := d.Repo.Erase(d.ctx(), id, actor)
	return n, gone(err)
}

// Receipts lists recent receipts for the picker.
func (d *Desk) Receipts() ([]calendar.Receipt, error) { return d.Source.Recent() }

// CompleteByReceipt completes the visit with a paid receipt.
func (d *Desk) CompleteByReceipt(appointmentID, saleID, actor string) (checkout.Outcome, error) {
	r, err := d.Proc.CompleteByReceipt(appointmentID, saleID, actor)
	return r.Outcome, err
}
