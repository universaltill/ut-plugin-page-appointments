package checkout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/universaltill/ut-plugin-page-appointments/src/engine"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

var t0 = time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)

func appt(state engine.State) engine.Appointment {
	return engine.Appointment{
		ID: "a1", Source: "own", ResourceID: "r1", State: state,
		Start: t0, End: t0.Add(time.Hour),
		Lines: []engine.Line{
			{SKU: "cut", Name: "Ladies cut", Qty: 1, Amount: money.FromMinor(2500)},
			{SKU: "col", Name: "Full colour", Qty: 2, Amount: money.FromMinor(9000)},
		},
		Deposit: money.FromMinor(1000), // reserved: must never leak into the basket
	}
}

func TestBuildLines_TwoServicesTwoLines(t *testing.T) {
	got, err := BuildLines(appt(engine.InService))
	if err != nil {
		t.Fatal(err)
	}
	want := []BasketLine{
		{SKU: "cut", Qty: 1, Ref: "appt:a1:0", QuoteMinor: 2500},
		{SKU: "col", Qty: 2, Ref: "appt:a1:1", QuoteMinor: 9000},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildLines_NoDepositNoNameInJSON(t *testing.T) {
	a := appt(engine.CheckedIn)
	b, _ := BuildLines(a)
	j, _ := json.Marshal(b)
	s := string(j)
	for _, bad := range []string{"deposit", "ladies", "colour", "name", "client", "1000"} {
		if strings.Contains(strings.ToLower(s), strings.ToLower(bad)) {
			t.Errorf("basket spec leaks %q: %s", bad, s)
		}
	}
	// the quote is display data, never a price override
	if strings.Contains(s, `"price`) {
		t.Errorf("price field present: %s", s)
	}
}

func TestBuildLines_Refusals(t *testing.T) {
	for _, st := range []engine.State{engine.Booked, engine.Confirmed, engine.Completed, engine.NoShow, engine.CancelledByShop} {
		if _, err := BuildLines(appt(st)); !errors.Is(err, ErrNotCheckoutState) {
			t.Errorf("%s: %v", st, err)
		}
	}
	p := appt(engine.CheckedIn)
	p.Source = "provider"
	if _, err := BuildLines(p); !errors.Is(err, ErrNotOwn) {
		t.Errorf("provider: %v", err)
	}
	e := appt(engine.CheckedIn)
	e.Lines = nil
	if _, err := BuildLines(e); !errors.Is(err, ErrNoLines) {
		t.Errorf("empty: %v", err)
	}
	q := appt(engine.CheckedIn)
	q.Lines[0].Qty = 0
	if _, err := BuildLines(q); !errors.Is(err, ErrBadLine) {
		t.Errorf("qty0: %v", err)
	}
	c := appt(engine.CheckedIn)
	c.ID = "a:1"
	if _, err := BuildLines(c); !errors.Is(err, ErrBadLine) {
		t.Errorf("colon id: %v", err)
	}
}

func TestParseRef(t *testing.T) {
	id, i, ok := ParseRef("appt:a1:1")
	if !ok || id != "a1" || i != 1 {
		t.Fatal(id, i, ok)
	}
	for _, bad := range []string{"", "a1:1", "appt:a1", "appt::1", "appt:a1:x", "appt:a1:-1", "appt:a:b:1", "other:a1:1"} {
		if _, _, ok := ParseRef(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

// ---- fakes

type fakeStore struct {
	mu      sync.Mutex
	appts   map[string]engine.Appointment
	events  []engine.Event
	outbox  map[string]Completion
	order   []string
	failCom bool
	// currency by appointment id; "" means GBP.
	currency map[string]string
}

func (s *fakeStore) Currency(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.currency[id]; c != "" {
		return c, nil
	}
	return "GBP", nil
}

func newStore(as ...engine.Appointment) *fakeStore {
	s := &fakeStore{appts: map[string]engine.Appointment{}, outbox: map[string]Completion{}}
	for _, a := range as {
		s.appts[a.ID] = a
	}
	return s
}
func (s *fakeStore) Appointment(id string) (engine.Appointment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.appts[id]
	return a, ok, nil
}
func (s *fakeStore) Commit(a engine.Appointment, evs []engine.Event, c Completion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failCom {
		return errors.New("db down")
	}
	s.appts[a.ID] = a
	s.events = append(s.events, evs...)
	if _, ok := s.outbox[a.ID]; !ok {
		s.order = append(s.order, a.ID)
	}
	s.outbox[a.ID] = c
	return nil
}
func (s *fakeStore) Pending() ([]Completion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Completion
	for _, id := range s.order {
		if c := s.outbox[id]; !c.Published {
			out = append(out, c)
		}
	}
	return out, nil
}
func (s *fakeStore) MarkPublished(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.outbox[id]
	c.Published = true
	s.outbox[id] = c
	return nil
}

type fakePub struct {
	mu    sync.Mutex
	codes []int // consumed per call; then 0
	got   []string
	names []string
}

func (p *fakePub) Publish(name string, payload []byte) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
	p.got = append(p.got, string(payload))
	if len(p.codes) > 0 {
		c := p.codes[0]
		p.codes = p.codes[1:]
		return c
	}
	return 0
}

type fakeReceipts map[string]bool

func (r fakeReceipts) Paid(id string) (bool, error) { return r[id], nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return t0.Add(2 * time.Hour) }

func newProc(st *fakeStore, pub *fakePub) *Processor {
	n := 0
	return &Processor{
		Store: st, Pub: pub, Clock: fixedClock{}, Policy: engine.DefaultPolicy(),
		IDs: func() string { n++; return fmt.Sprintf("ev%d", n) },
		StaffRef: func(r string) string {
			if r == "r1" {
				return "staff-9"
			}
			return ""
		},
		Receipts: fakeReceipts{"sale-1": true},
	}
}

func sale(id string, refs ...string) SaleCompleted {
	s := SaleCompleted{SaleID: id}
	for _, r := range refs {
		s.LineItems = append(s.LineItems, SaleLine{SKU: "x", Qty: 1, Ref: r})
	}
	return s
}

func TestHandleSale_CompletesOnceEvenIfDeliveredTwice(t *testing.T) {
	st := newStore(appt(engine.InService))
	pub := &fakePub{}
	p := newProc(st, pub)
	s := sale("sale-1", "appt:a1:0", "appt:a1:1")

	r1, err := p.HandleSaleCompleted(s, "u1")
	if err != nil || len(r1) != 1 || r1[0].Outcome != Completed {
		t.Fatalf("%+v %v", r1, err)
	}
	r2, err := p.HandleSaleCompleted(s, "u1")
	if err != nil || len(r2) != 1 || r2[0].Outcome != Duplicate {
		t.Fatalf("%+v %v", r2, err)
	}
	a, _, _ := st.Appointment("a1")
	if a.State != engine.Completed || a.SaleRef != "sale-1" {
		t.Fatalf("%+v", a)
	}
	if len(st.events) != 1 {
		t.Fatalf("events %+v", st.events)
	}
	if len(pub.got) != 1 {
		t.Fatalf("published %d times", len(pub.got))
	}
	if pub.names[0] != EventName {
		t.Fatal(pub.names[0])
	}
}

func TestHandleSale_CheckedInGetsTwoStepTransition(t *testing.T) {
	st := newStore(appt(engine.CheckedIn))
	p := newProc(st, &fakePub{})
	if _, err := p.HandleSaleCompleted(sale("sale-1", "appt:a1:0", "appt:a1:1"), "u1"); err != nil {
		t.Fatal(err)
	}
	if len(st.events) != 2 || st.events[0].ToState != engine.InService || st.events[1].ToState != engine.Completed {
		t.Fatalf("%+v", st.events)
	}
}

func TestHandleSale_NotMatchingCases(t *testing.T) {
	st := newStore(appt(engine.InService))
	p := newProc(st, &fakePub{})
	// foreign refs and empty ref lines are ignored
	r, err := p.HandleSaleCompleted(sale("sale-1", "", "garbage", "appt:zz:0"), "u1")
	if err != nil || len(r) != 1 || r[0].AppointmentID != "zz" || r[0].Outcome != Unknown {
		t.Fatalf("%+v %v", r, err)
	}
	// partial: only line 0 of two
	r, err = p.HandleSaleCompleted(sale("sale-1", "appt:a1:0"), "u1")
	if err != nil || r[0].Outcome != Incomplete {
		t.Fatalf("%+v %v", r, err)
	}
	a, _, _ := st.Appointment("a1")
	if a.State != engine.InService {
		t.Fatal("must stay in_service for manual completion")
	}
	// sale without any appointment ref: no results
	r, _ = p.HandleSaleCompleted(sale("sale-1"), "u1")
	if len(r) != 0 {
		t.Fatal(r)
	}
	// empty sale id refused
	if _, err := p.HandleSaleCompleted(sale("", "appt:a1:0"), "u1"); !errors.Is(err, engine.ErrSaleRefRequired) {
		t.Fatal(err)
	}
}

func TestHandleSale_RefsInAnyOrderAndRepeatedRefs(t *testing.T) {
	st := newStore(appt(engine.InService))
	p := newProc(st, &fakePub{})
	r, err := p.HandleSaleCompleted(sale("sale-1", "appt:a1:1", "appt:a1:0", "appt:a1:1"), "u1")
	if err != nil || len(r) != 1 || r[0].Outcome != Completed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestHandleSale_DifferentSaleOnCompletedIsConflict(t *testing.T) {
	st := newStore(appt(engine.InService))
	p := newProc(st, &fakePub{})
	p.HandleSaleCompleted(sale("sale-1", "appt:a1:0", "appt:a1:1"), "u1")
	r, err := p.HandleSaleCompleted(sale("sale-2", "appt:a1:0", "appt:a1:1"), "u1")
	if err != nil || r[0].Outcome != OtherSale {
		t.Fatalf("%+v %v", r, err)
	}
	a, _, _ := st.Appointment("a1")
	if a.SaleRef != "sale-1" {
		t.Fatal("sale ref overwritten")
	}
}

func TestHandleSale_NotInCheckoutStateIsSkipped(t *testing.T) {
	st := newStore(appt(engine.CancelledByShop))
	p := newProc(st, &fakePub{})
	r, err := p.HandleSaleCompleted(sale("sale-1", "appt:a1:0", "appt:a1:1"), "u1")
	if err != nil || r[0].Outcome != NotCompletable {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestPublish_RedeliverySafe(t *testing.T) {
	st := newStore(appt(engine.InService))
	pub := &fakePub{codes: []int{-5}} // rate limited: not published
	p := newProc(st, pub)
	if _, err := p.HandleSaleCompleted(sale("sale-1", "appt:a1:0", "appt:a1:1"), "u1"); err != nil {
		t.Fatal(err)
	}
	if pend, _ := st.Pending(); len(pend) != 1 {
		t.Fatalf("must stay pending after -5: %+v", pend)
	}
	// next event of any kind flushes
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}
	if pend, _ := st.Pending(); len(pend) != 0 {
		t.Fatal("still pending")
	}
	if len(pub.got) != 2 || pub.got[0] != pub.got[1] {
		t.Fatalf("payload must be identical on republish: %v", pub.got)
	}
	// flush again: nothing
	p.Flush()
	if len(pub.got) != 2 {
		t.Fatal("published after success")
	}
}

func TestCommitFailureLeavesNothingPublished(t *testing.T) {
	st := newStore(appt(engine.InService))
	st.failCom = true
	pub := &fakePub{}
	p := newProc(st, pub)
	if _, err := p.HandleSaleCompleted(sale("sale-1", "appt:a1:0", "appt:a1:1"), "u1"); err == nil {
		t.Fatal("want error")
	}
	if len(pub.got) != 0 {
		t.Fatal("published before commit")
	}
}

func TestPayload(t *testing.T) {
	a := appt(engine.Completed)
	a.SaleRef = "sale-1"
	b, err := BuildPayload(a, "GBP", "staff-9")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"appointment_id":"a1","resource_id":"r1","logbook_staff_ref":"staff-9","currency":"GBP","lines":[{"sku":"cut","name_snapshot":"Ladies cut","qty":1,"amount_minor":2500},{"sku":"col","name_snapshot":"Full colour","qty":2,"amount_minor":9000}],"sale_ref":"sale-1"}`
	if string(b) != want {
		t.Fatalf("\n got %s\nwant %s", b, want)
	}
	b, _ = BuildPayload(a, "GBP", "")
	if strings.Contains(string(b), "logbook_staff_ref") {
		t.Fatal("unmapped must omit the staff ref")
	}
	a.SaleRef = ""
	if _, err := BuildPayload(a, "GBP", ""); !errors.Is(err, engine.ErrSaleRefRequired) {
		t.Fatal(err)
	}
	a.SaleRef = "s"
	if _, err := BuildPayload(a, "", ""); !errors.Is(err, ErrBadLine) {
		t.Fatal(err)
	}
	// no client name, deposit or notes anywhere
	a.SaleRef = "sale-1"
	b, _ = BuildPayload(a, "GBP", "")
	for _, bad := range []string{"deposit", "client", "note", "1000"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("payload leaks %q", bad)
		}
	}
}

func TestEventNameMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct{ ID string }
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if EventName != m.ID+".appointment.completed" {
		t.Fatalf("%s vs %s", EventName, m.ID)
	}
}

func TestCompleteByReceipt(t *testing.T) {
	st := newStore(appt(engine.InService))
	pub := &fakePub{}
	p := newProc(st, pub)

	// unknown receipt refused, nothing changes
	if _, err := p.CompleteByReceipt("a1", "sale-404", "u1"); !errors.Is(err, ErrReceiptNotPaid) {
		t.Fatal(err)
	}
	// blank refused
	if _, err := p.CompleteByReceipt("a1", " ", "u1"); !errors.Is(err, engine.ErrSaleRefRequired) {
		t.Fatal(err)
	}
	if _, err := p.CompleteByReceipt("nope", "sale-1", "u1"); !errors.Is(err, ErrUnknownAppointment) {
		t.Fatal(err)
	}
	res, err := p.CompleteByReceipt("a1", "sale-1", "u1")
	if err != nil || res.Outcome != Completed {
		t.Fatalf("%+v %v", res, err)
	}
	if len(pub.got) != 1 {
		t.Fatal("not published")
	}
	// picking the same receipt again is a no-op, another receipt is refused
	res, err = p.CompleteByReceipt("a1", "sale-1", "u1")
	if err != nil || res.Outcome != Duplicate {
		t.Fatalf("%+v %v", res, err)
	}
	p.Receipts = fakeReceipts{"sale-1": true, "sale-2": true}
	res, _ = p.CompleteByReceipt("a1", "sale-2", "u1")
	if res.Outcome != OtherSale {
		t.Fatalf("%+v", res)
	}
}

func TestCompleteByReceipt_FromBookedRefused(t *testing.T) {
	st := newStore(appt(engine.Booked))
	p := newProc(st, &fakePub{})
	r, err := p.CompleteByReceipt("a1", "sale-1", "u1")
	if err != nil || r.Outcome != NotCompletable {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestConcurrentRedelivery_RaceFree(t *testing.T) {
	st := newStore(appt(engine.InService))
	pub := &fakePub{}
	p := newProc(st, pub)
	s := sale("sale-1", "appt:a1:0", "appt:a1:1")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.HandleSaleCompleted(s, "u1"); err != nil {
				t.Error(err)
			}
			_ = p.Flush()
		}()
	}
	wg.Wait()
	if len(st.events) != 1 || len(pub.got) != 1 {
		t.Fatalf("events %d published %d", len(st.events), len(pub.got))
	}
}

// N5: the completed event carries the appointment record's own currency.
func TestCompleted_UsesTheRecordCurrency_N5(t *testing.T) {
	st := newStore(appt(engine.InService))
	st.currency = map[string]string{"a1": "EUR"}
	pub := &fakePub{}
	p := newProc(st, pub)
	if _, err := p.CompleteByReceipt("a1", "sale-1", "u1"); err != nil {
		t.Fatal(err)
	}
	if len(pub.got) != 1 || !strings.Contains(pub.got[0], `"currency":"EUR"`) {
		t.Fatalf("published %+v", pub.got)
	}
}
