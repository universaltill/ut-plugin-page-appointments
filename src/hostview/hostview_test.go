//go:build !wasip1

package hostview_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/hostview"
)

func fake(t *testing.T, views func(string, json.RawMessage) (json.RawMessage, error)) {
	h := plugin.NewFakeHost()
	h.Views = views
	plugin.UseFakeHost(t, h)
}

func TestCatalogueFindsSKUsAcrossPages(t *testing.T) {
	calls := 0
	fake(t, func(name string, args json.RawMessage) (json.RawMessage, error) {
		calls++
		var a struct{ Offset, Limit int }
		_ = json.Unmarshal(args, &a)
		if name != "catalog.items.v1" || a.Limit != 500 {
			t.Fatalf("view %s args %s", name, args)
		}
		if a.Offset == 0 {
			rows := make([]map[string]any, 500)
			for i := range rows {
				rows[i] = map[string]any{"sku": "x" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "name": "n", "price_minor": 1, "active": true}
			}
			return json.Marshal(rows)
		}
		return json.RawMessage(`[{"sku":"cut","name":"Cut","price_minor":2500,"active":true},{"sku":"old","name":"Old","price_minor":100,"active":false}]`), nil
	})
	got, err := hostview.Catalogue{}.Items([]string{"cut", "old", "missing"})
	if err != nil || calls != 2 || got["cut"].Price.Minor() != 2500 || !got["cut"].Active || got["old"].Active {
		t.Fatalf("items = %+v calls %d err %v", got, calls, err)
	}
	if _, ok := got["missing"]; ok {
		t.Fatal("unknown sku returned")
	}
}

func TestReceiptsNewestFirstRefundsMarkedPaidChecks(t *testing.T) {
	fake(t, func(name string, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`[{"id":"s1","receipt_no":"1","sale_type":"sale","created_at":"2026-01-14T09:00:00Z","total_minor":500},
			{"id":"s2","receipt_no":"2","sale_type":"return","created_at":"2026-01-14 10:00:00","total_minor":-500}]`), nil
	})
	rs, err := hostview.Receipts{}.Recent()
	if err != nil || len(rs) != 2 || rs[0].ID != "s2" || !rs[0].IsRefund || rs[0].At.Hour() != 10 || rs[1].Total.Minor() != 500 {
		t.Fatalf("recent = %+v %v", rs, err)
	}
	if ok, _ := (hostview.Receipts{}).Paid("s1"); !ok {
		t.Fatal("s1 not paid")
	}
	if ok, _ := (hostview.Receipts{}).Paid("s2"); ok {
		t.Fatal("a return counted as paid")
	}
	if ok, _ := (hostview.Receipts{}).Paid("nope"); ok {
		t.Fatal("unknown receipt counted as paid")
	}
}

func TestShopCurrency(t *testing.T) {
	fake(t, func(string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`[{"currency_code":"EUR","currency_decimals":2}]`), nil
	})
	if c, err := hostview.ShopCurrency(); err != nil || c != "EUR" {
		t.Fatalf("%q %v", c, err)
	}
}

// dayOf fakes sales.receipts.v1 over n receipts (oldest first, ids r0..r<n-1>)
// with the view's real arg bounds, counting calls.
func dayOf(t *testing.T, n int, calls *int) {
	fake(t, func(name string, args json.RawMessage) (json.RawMessage, error) {
		*calls++
		var a struct{ DaysAgo, Offset, Limit int }
		if err := json.Unmarshal(args, &a); err != nil {
			t.Fatal(err)
		}
		if name != "sales.receipts.v1" || a.Limit < 1 || a.Limit > 100 || a.Offset < 0 || a.Offset > 1000000 {
			t.Fatalf("view %s args %s", name, args)
		}
		rows := []map[string]any{}
		for i := a.Offset; i < n && i < a.Offset+a.Limit; i++ {
			rows = append(rows, map[string]any{"id": fmt.Sprintf("r%d", i), "receipt_no": fmt.Sprint(i), "sale_type": "sale",
				"created_at": "2026-01-14T09:00:00Z", "total_minor": 100})
		}
		return json.Marshal(rows)
	})
}

// A busy day (more receipts than the old 500 cap): the picker lists the
// newest, newest first, within the per-request view budget (fix-plug
// finding 5). The view is oldest-first only, so the plugin finds the tail.
func TestReceiptsBusyDayListsTheNewest(t *testing.T) {
	for _, n := range []int{0, 1, 99, 100, 101, 250, 500, 1234, 9999} {
		calls := 0
		dayOf(t, n, &calls)
		rs, err := hostview.Receipts{}.Recent()
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if n == 0 {
			if len(rs) != 0 {
				t.Fatalf("n=0: %+v", rs)
			}
			continue
		}
		if rs[0].ID != fmt.Sprintf("r%d", n-1) {
			t.Fatalf("n=%d: newest = %s (calls %d)", n, rs[0].ID, calls)
		}
		want := n
		if want > 100 {
			want = 100
		}
		if len(rs) < want || (len(rs) > 1 && rs[1].ID != fmt.Sprintf("r%d", n-2)) {
			t.Fatalf("n=%d: %d rows, second %s", n, len(rs), rs[1].ID)
		}
		if calls > hostview.MaxReceiptCalls {
			t.Fatalf("n=%d: %d view calls", n, calls)
		}
		// the newest receipt is found as paid
		calls = 0
		if ok, err := (hostview.Receipts{}).Paid(fmt.Sprintf("r%d", n-1)); !ok || err != nil || calls > hostview.MaxReceiptCalls {
			t.Fatalf("n=%d: newest not paid: %v %v calls %d", n, ok, err, calls)
		}
	}
}

// #7267: a role without view:sales (a cashier) gets the typed refusal, not a
// host error page.
func TestReceiptsDeniedIsTyped(t *testing.T) {
	fake(t, func(string, json.RawMessage) (json.RawMessage, error) { return nil, plugin.ErrDenied })
	if _, err := (hostview.Receipts{}).Recent(); !errors.Is(err, calendar.ErrReceiptsDenied) {
		t.Fatalf("Recent: %v", err)
	}
	if _, err := (hostview.Receipts{}).Paid("s1"); !errors.Is(err, calendar.ErrReceiptsDenied) {
		t.Fatalf("Paid: %v", err)
	}
}
