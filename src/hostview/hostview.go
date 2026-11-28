// Package hostview reads the core views the plugin needs through the
// view_query host function (ADR-0121 section 5, reference/contracts/
// plugin-views.md): the catalogue (catalog.items.v1, view:inventory), the
// day's receipts (sales.receipts.v1, view:sales) and the shop's facts
// (shop.context.v2, view:shop). Each name must also be in the manifest's
// views_used. A view runs only inside an operator's ui ask; a schedule tick is
// refused by the host, so nothing here is called from one.
package hostview

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/universaltill/universal-till/sdk/plugin"

	"github.com/universaltill/ut-plugin-page-appointments/src/adapter"
	"github.com/universaltill/ut-plugin-page-appointments/src/calendar"
	"github.com/universaltill/ut-plugin-page-appointments/src/money"
)

// Query is the one host call used (plugin.ViewQuery); tests replace it.
var Query = plugin.ViewQuery

const (
	catalogPage = 500
	// maxCatalogPages bounds the walk: 20 pages are 10,000 items.
	maxCatalogPages = 20
	receiptPage     = 100
	maxReceiptPages = 5
)

// Catalogue is adapter.Catalogue over catalog.items.v1.
type Catalogue struct{}

var _ adapter.Catalogue = Catalogue{}

type catalogRow struct {
	SKU    string `json:"sku"`
	Name   string `json:"name"`
	Price  int64  `json:"price_minor"`
	Active bool   `json:"active"`
}

// Items pages the catalogue until every wanted SKU is found or it ends.
func (Catalogue) Items(skus []string) (map[string]adapter.Item, error) {
	want := map[string]bool{}
	for _, s := range skus {
		want[s] = true
	}
	out := map[string]adapter.Item{}
	for page := 0; page < maxCatalogPages && len(out) < len(want); page++ {
		raw, err := Query("catalog.items.v1", map[string]int{"offset": page * catalogPage, "limit": catalogPage})
		if err != nil {
			return nil, fmt.Errorf("hostview: catalog.items.v1: %w", err)
		}
		var rows []catalogRow
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("hostview: catalog.items.v1: decode: %w", err)
		}
		for _, r := range rows {
			if want[r.SKU] && r.SKU != "" {
				if _, dup := out[r.SKU]; !dup {
					out[r.SKU] = adapter.Item{Name: r.Name, Price: money.FromMinor(r.Price), Active: r.Active}
				}
			}
		}
		if len(rows) < catalogPage {
			break
		}
	}
	return out, nil
}

// Receipts is adapter.Receipts over sales.receipts.v1 (today's business day).
type Receipts struct{}

var _ adapter.Receipts = Receipts{}

type receiptRow struct {
	ID        string `json:"id"`
	No        string `json:"receipt_no"`
	SaleType  string `json:"sale_type"`
	CreatedAt string `json:"created_at"`
	Total     int64  `json:"total_minor"`
}

func (Receipts) day() ([]receiptRow, error) {
	var all []receiptRow
	for page := 0; page < maxReceiptPages; page++ {
		raw, err := Query("sales.receipts.v1", map[string]int{"days_ago": 0, "offset": page * receiptPage, "limit": receiptPage})
		if err != nil {
			return nil, fmt.Errorf("hostview: sales.receipts.v1: %w", err)
		}
		var rows []receiptRow
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("hostview: sales.receipts.v1: decode: %w", err)
		}
		all = append(all, rows...)
		if len(rows) < receiptPage {
			break
		}
	}
	return all, nil
}

func parseTime(s string) time.Time {
	for _, l := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// Recent lists today's receipts, newest first.
func (r Receipts) Recent() ([]calendar.Receipt, error) {
	rows, err := r.day()
	if err != nil {
		return nil, err
	}
	out := make([]calendar.Receipt, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // the view is oldest first
		w := rows[i]
		out = append(out, calendar.Receipt{ID: w.ID, No: w.No, At: parseTime(w.CreatedAt),
			Total: money.FromMinor(w.Total), IsRefund: w.SaleType == "return"})
	}
	return out, nil
}

// Paid reports whether saleID is a completed sale of today (not a return).
// A visit is completed with a receipt of the business day it was paid on.
func (r Receipts) Paid(saleID string) (bool, error) {
	rows, err := r.day()
	if err != nil {
		return false, err
	}
	for _, w := range rows {
		if w.ID == saleID {
			return w.SaleType != "return", nil
		}
	}
	return false, nil
}

// ShopCurrency returns the till's currency code (shop.context.v2).
func ShopCurrency() (string, error) {
	raw, err := Query("shop.context.v2", nil)
	if err != nil {
		return "", fmt.Errorf("hostview: shop.context.v2: %w", err)
	}
	var rows []struct {
		Code string `json:"currency_code"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 1 {
		return "", fmt.Errorf("hostview: shop.context.v2: unexpected answer %q", raw)
	}
	return rows[0].Code, nil
}
