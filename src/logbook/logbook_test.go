package logbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

const good = `{"appointment_id":"a1","resource_id":"r1","logbook_staff_ref":"staff-9","currency":"GBP","lines":[{"sku":"cut","name_snapshot":"Ladies cut","qty":1,"amount_minor":2500},{"sku":"col","name_snapshot":"Full colour","qty":2,"amount_minor":9000}],"sale_ref":"sale-1"}`

func TestParse_Good(t *testing.T) {
	e, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if e.AppointmentID != "a1" || e.StaffRef != "staff-9" || e.SaleRef != "sale-1" || e.Currency != "GBP" || len(e.Lines) != 2 || e.Lines[1].Qty != 2 || e.Lines[1].Amount.Minor() != 9000 {
		t.Fatalf("%+v", e)
	}
}

func TestParse_Refusals(t *testing.T) {
	cases := map[string]string{
		"empty":        ``,
		"not json":     `{`,
		"trailing":     good + `{}`,
		"no id":        `{"resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":1}],"sale_ref":"s"}`,
		"no sale":      `{"appointment_id":"a1","resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":1}]}`,
		"no currency":  `{"appointment_id":"a1","resource_id":"r1","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":1}],"sale_ref":"s"}`,
		"no lines":     `{"appointment_id":"a1","resource_id":"r1","currency":"GBP","lines":[],"sale_ref":"s"}`,
		"qty zero":     `{"appointment_id":"a1","resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":0,"amount_minor":1}],"sale_ref":"s"}`,
		"neg amount":   `{"appointment_id":"a1","resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":-1}],"sale_ref":"s"}`,
		"overflow sum": `{"appointment_id":"a1","resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":9000000000000000000},{"sku":"b","name_snapshot":"B","qty":1,"amount_minor":9000000000000000000}],"sale_ref":"s"}`,
	}
	for n, c := range cases {
		if _, err := Parse([]byte(c)); !errors.Is(err, ErrBadEvent) {
			t.Errorf("%s: %v", n, err)
		}
	}
}

type staffSet map[string]bool

func (s staffSet) Has(ref string) bool { return s[ref] }

type memStore struct {
	mu      sync.Mutex
	entries map[string]Entry
	fail    bool
}

func (m *memStore) InsertIfAbsent(e Entry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return false, errors.New("db")
	}
	if m.entries == nil {
		m.entries = map[string]Entry{}
	}
	if _, ok := m.entries[e.AppointmentID]; ok {
		return false, nil
	}
	m.entries[e.AppointmentID] = e
	return true, nil
}

func TestHandle_WritesOneEntryAndIsIdempotent(t *testing.T) {
	st := &memStore{}
	c := &Consumer{Store: st, Staff: staffSet{"staff-9": true}}
	r, err := c.Handle([]byte(good))
	if err != nil || r.Outcome != Written || r.Entry.Total.Minor() != 11500 || r.Entry.StaffRef != "staff-9" {
		t.Fatalf("%+v %v", r, err)
	}
	if r.Entry.SaleRef != "sale-1" || r.Entry.Currency != "GBP" || len(r.Entry.Lines) != 2 {
		t.Fatalf("%+v", r.Entry)
	}
	r, err = c.Handle([]byte(good))
	if err != nil || r.Outcome != Duplicate {
		t.Fatalf("%+v %v", r, err)
	}
	if len(st.entries) != 1 {
		t.Fatal(len(st.entries))
	}
}

func TestHandle_UnmappedWritesNoneAndSaysSo(t *testing.T) {
	for name, body := range map[string]string{
		"no ref":        `{"appointment_id":"a2","resource_id":"r1","currency":"GBP","lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":1}],"sale_ref":"s"}`,
		"unknown staff": good, // ref present but the logbook has no such staff
	} {
		st := &memStore{}
		c := &Consumer{Store: st, Staff: staffSet{}}
		r, err := c.Handle([]byte(body))
		if err != nil || r.Outcome != Unmapped || r.MessageKey != KeyUnmapped {
			t.Fatalf("%s: %+v %v", name, r, err)
		}
		if len(st.entries) != 0 {
			t.Fatalf("%s: wrote an entry", name)
		}
	}
}

func TestHandle_BadPayloadAndStoreError(t *testing.T) {
	c := &Consumer{Store: &memStore{}, Staff: staffSet{"staff-9": true}}
	if _, err := c.Handle([]byte(`{}`)); !errors.Is(err, ErrBadEvent) {
		t.Fatal(err)
	}
	c = &Consumer{Store: &memStore{fail: true}, Staff: staffSet{"staff-9": true}}
	if _, err := c.Handle([]byte(good)); err == nil {
		t.Fatal("want store error")
	}
}

func TestHandle_ConcurrentRedeliveryOneEntry(t *testing.T) {
	st := &memStore{}
	c := &Consumer{Store: st, Staff: staffSet{"staff-9": true}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	written := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Handle([]byte(good))
			if err != nil {
				t.Error(err)
			}
			if r.Outcome == Written {
				mu.Lock()
				written++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if written != 1 || len(st.entries) != 1 {
		t.Fatalf("written %d entries %d", written, len(st.entries))
	}
}

func TestEntryID_DeterministicFromAppointment(t *testing.T) {
	a, _ := Parse([]byte(good))
	if EntryID(a.AppointmentID) != EntryID("a1") || EntryID("a1") == EntryID("a2") {
		t.Fatal("not deterministic")
	}
	if got := EntryID("a1"); len(got) != 36 {
		t.Fatalf("want UUID shape: %s", got)
	}
}

func TestLocaleKeyExists(t *testing.T) {
	raw, err := os.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m[KeyUnmapped] == "" {
		t.Fatal("missing " + KeyUnmapped)
	}
}

// ADR-0039 allows additive payload fields: an unknown key is ignored and is
// never copied into the entry (review N15).
func TestParse_IgnoresUnknownFields(t *testing.T) {
	p := `{"appointment_id":"a1","resource_id":"r1","logbook_staff_ref":"s","currency":"GBP","client_first_name":"Zed","future":{"x":1},"lines":[{"sku":"a","name_snapshot":"A","qty":1,"amount_minor":5,"extra":true}],"sale_ref":"s"}`
	e, err := Parse([]byte(p))
	if err != nil || e.AppointmentID != "a1" || len(e.Lines) != 1 || e.Lines[0].Amount.Minor() != 5 {
		t.Fatalf("%+v %v", e, err)
	}
	if strings.Contains(fmt.Sprintf("%+v", e), "Zed") {
		t.Fatal("an unknown field reached the event")
	}
}
