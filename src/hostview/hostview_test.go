//go:build !wasip1

package hostview_test

import (
	"encoding/json"
	"testing"

	"github.com/universaltill/universal-till/sdk/plugin"

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
