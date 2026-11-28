package adapter_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/adapter"
	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/checkout"
	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

type pub struct {
	code int
	got  [][]byte
}

func (p *pub) Publish(name string, payload []byte) int {
	if p.code == 0 {
		p.got = append(p.got, payload)
	}
	return p.code
}

type recs struct{ paid map[string]bool }

func (r recs) Paid(id string) (bool, error) { return r.paid[id], nil }
func (r recs) Recent() ([]calendar.Receipt, error) {
	return []calendar.Receipt{
		{ID: "sale-9", No: "0009", At: time.Date(2026, 1, 14, 10, 40, 0, 0, time.UTC), Total: money.FromMinor(2500)},
		{ID: "sale-8", No: "0008", At: time.Date(2026, 1, 14, 10, 30, 0, 0, time.UTC), Total: money.FromMinor(-2500), IsRefund: true},
	}, nil
}

func deskRig(t *testing.T) (*rig, *adapter.Desk, *pub, *adapter.Checkout) {
	r := newRig(t)
	p := &pub{}
	co := &adapter.Checkout{Repo: r.repo, StaffRef: func(string) string { return "staff-1" }}
	proc := &checkout.Processor{Store: co, Pub: p, Receipts: recs{paid: map[string]bool{"sale-9": true}}, Clock: r.clk,
		IDs: r.d.IDs, Policy: engine.DefaultPolicy(), StaffRef: co.StaffRef}
	desk := &adapter.Desk{Store: r.st, Proc: proc, Source: recs{paid: map[string]bool{"sale-9": true}}}
	r.d.Store = desk
	return r, desk, p, co
}

func TestReceiptPickerCompletesAndPublishesOnce(t *testing.T) {
	r, _, p, co := deskRig(t)
	b := r.bookAt(10, "Ana")
	r.clk.t = time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	r.ask("do_checkin_"+tok(b.ID), mgr, nil)
	r.ask("do_start_"+tok(b.ID), mgr, nil)
	pick := r.ask("receipt_"+tok(b.ID), mgr, nil)
	if d := r.doc(pick); !strings.Contains(d, `"value":"sale-9"`) || strings.Contains(d, "sale-8") {
		t.Fatalf("picker lists %s", d)
	}
	r.ask("finish_"+tok(b.ID), mgr, map[string]any{"receipt": "sale-9"})
	g, _ := r.repo.Get(context.Background(), b.ID)
	if g.State != engine.Completed || g.SaleRef != "sale-9" {
		t.Fatalf("after finish: %s %q", g.State, g.SaleRef)
	}
	if len(p.got) != 1 || !strings.Contains(string(p.got[0]), `"currency":"GBP"`) || !strings.Contains(string(p.got[0]), `"logbook_staff_ref":"staff-1"`) {
		t.Fatalf("published %q", p.got)
	}
	if pend, _ := co.Pending(); len(pend) != 0 {
		t.Fatalf("pending after publish: %d", len(pend))
	}
	// a repeated post is a no-op, nothing published twice
	r.ask("finish_"+tok(b.ID), mgr, map[string]any{"receipt": "sale-9"})
	if len(p.got) != 1 {
		t.Fatalf("published %d times", len(p.got))
	}
	// an unpaid receipt is refused with a notice, the visit stays in service
}

func TestReceiptNotPaidRefused(t *testing.T) {
	r, _, p, _ := deskRig(t)
	b := r.bookAt(10, "Ana")
	r.clk.t = time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	r.ask("do_checkin_"+tok(b.ID), mgr, nil)
	ans := r.ask("finish_"+tok(b.ID), mgr, map[string]any{"receipt": "sale-unknown"})
	if !strings.Contains(r.doc(ans), "receipt_not_paid") {
		t.Fatalf("no refusal notice: %s", r.doc(ans))
	}
	if g, _ := r.repo.Get(context.Background(), b.ID); g.State != engine.CheckedIn || len(p.got) != 0 {
		t.Fatalf("state %s published %d", g.State, len(p.got))
	}
}

// A crash after the state moved and before the outbox write: the completed
// event is rebuilt from the record and published on the next event.
func TestCompletedWithoutOutboxIsRebuiltAndPublished(t *testing.T) {
	r, _, p, co := deskRig(t)
	b := r.bookAt(10, "Ana")
	r.clk.t = time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	r.ask("do_checkin_"+tok(b.ID), mgr, nil)
	// drop the outbox insert, as a crash would
	r.db.FailExec = func(q string, _ []any) error {
		if strings.Contains(q, "completion_outbox") {
			return errors.New("crash")
		}
		return nil
	}
	_, herr := r.d.Handle(calendar.Ask{View: calendar.ViewDay, Action: "finish_" + tok(b.ID), Form: map[string]any{"receipt": "sale-9"},
		Locale: "en", Operator: mgr, Now: r.clk.t}) // the host shows its own error page; the state already moved
	if herr == nil {
		t.Fatal("the crash was not reported")
	}
	r.db.FailExec = nil
	g, _ := r.repo.Get(context.Background(), b.ID)
	if g.State != engine.Completed {
		t.Fatalf("completion did not reach the state step: %s", g.State)
	}
	pend, err := co.Pending()
	if err != nil || len(pend) != 1 || pend[0].AppointmentID != b.ID {
		t.Fatalf("pending = %+v %v", pend, err)
	}
	proc := &checkout.Processor{Store: co, Pub: p, Receipts: recs{}, Clock: r.clk, IDs: r.d.IDs, Policy: engine.DefaultPolicy()}
	if err := proc.Flush(); err != nil || len(p.got) != 1 {
		t.Fatalf("flush: %v published %d", err, len(p.got))
	}
}

func TestEraseBlanksTheChainAndNeedsManager(t *testing.T) {
	r, _, _, _ := deskRig(t)
	b := r.bookAt(10, "Ana")
	cashier := &calendar.Operator{UserID: "u-c", Role: "cashier"}
	if d := r.doc(r.ask("open_"+tok(b.ID), cashier, nil)); strings.Contains(d, "erasecheck_") {
		t.Fatal("cashier is offered the erase button")
	}
	r.ask("erase_"+tok(b.ID), cashier, nil)
	if g, _ := r.repo.Get(context.Background(), b.ID); g.FirstName != "Ana" {
		t.Fatal("a cashier erased the name")
	}
	if d := r.doc(r.ask("open_"+tok(b.ID), mgr, nil)); !strings.Contains(d, "erasecheck_") {
		t.Fatal("manager is not offered the erase button")
	}
	r.ask("erase_"+tok(b.ID), mgr, nil)
	if g, _ := r.repo.Get(context.Background(), b.ID); g.FirstName != "" {
		t.Fatalf("name still %q", g.FirstName)
	}
	evs, _ := r.repo.Events(context.Background(), b.ID)
	if last := evs[len(evs)-1]; last.Kind != "purge" || last.Reason != "erasure" || last.ActorUserID != "u-mgr" {
		t.Fatalf("last event %+v", last)
	}
	if strings.Contains(r.db.Dump(), "Ana") {
		t.Fatal("the name is still in the database")
	}
}
