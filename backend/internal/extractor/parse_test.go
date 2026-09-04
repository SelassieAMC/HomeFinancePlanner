package extractor

import (
	"testing"
)

func TestParseBillJSON_PinnedSchema(t *testing.T) {
	raw := `{
		"market_name": " FreshMart ",
		"date": "05/03/2026",
		"payment_method": "CARD",
		"card_last_digits": "****4321",
		"items": [
			{"name": "Milk 1L", "brand": "Lactona", "category": "Dairy", "quantity": 2, "unit_price": 1.50, "discount": 0, "line_total": 3.00},
			{"name": "Bread", "quantity": 1, "unit_price": 2.20, "discount": 0.20}
		],
		"items_total": 5.00,
		"discount_total": 0.50,
		"vat_total": 0.45,
		"total_paid": 5.45
	}`

	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.MarketName != "FreshMart" {
		t.Errorf("market name: %q", draft.MarketName)
	}
	if draft.Date != "2026-03-05" {
		t.Errorf("date: %q (want 2026-03-05, DD/MM/YYYY input)", draft.Date)
	}
	if draft.PaymentMethod != "card" {
		t.Errorf("payment method: %q", draft.PaymentMethod)
	}
	if draft.CardLastDigits != "4321" { // masked digits normalized
		t.Errorf("card digits: %q", draft.CardLastDigits)
	}
	if len(draft.Items) != 2 {
		t.Fatalf("items: %d", len(draft.Items))
	}
	if draft.Items[0].LineTotalCents != 300 {
		t.Errorf("line total cents: %d", draft.Items[0].LineTotalCents)
	}
	if draft.Items[0].Brand != "Lactona" || draft.Items[0].CategoryName != "dairy" {
		t.Errorf("item meta: brand=%q category=%q", draft.Items[0].Brand, draft.Items[0].CategoryName)
	}
	if draft.Items[1].Quantity != 1 { // missing quantity defaults to 1
		t.Errorf("default quantity: %v", draft.Items[1].Quantity)
	}
	// The total is the sum of the lines (VAT is already included in the item
	// prices and is informational only); the printed value is kept separately
	// for the mismatch warning.
	if draft.ItemsSubtotalCents != 500 || draft.DiscountCents != 50 || draft.VATCents != 45 || draft.TotalCents != 500 {
		t.Errorf("totals: subtotal=%d discount=%d vat=%d total=%d",
			draft.ItemsSubtotalCents, draft.DiscountCents, draft.VATCents, draft.TotalCents)
	}
	if draft.PrintedTotalCents != 545 {
		t.Errorf("printed total: %d", draft.PrintedTotalCents)
	}
}

func TestParseBillJSON_PrintedTotalDrivesWarning(t *testing.T) {
	// A printed total that does not match the lines is never used as the
	// total — it is kept so the UI can warn about the mismatch.
	raw := `{"market_name":"M","date":"2026-05-03","payment_method":"card",
		"items":[{"name":"Milk","quantity":1,"unit_price":5.00,"line_total":5.00}],
		"vat_total":0.45,"total_paid":5.99}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.TotalCents != 500 { // computed: sum of lines only (VAT informational)
		t.Errorf("computed total: %d (want 500)", draft.TotalCents)
	}
	if draft.PrintedTotalCents != 599 { // kept verbatim for the warning
		t.Errorf("printed total: %d (want 599)", draft.PrintedTotalCents)
	}
}

func TestParseBillJSON_StripsFencesAndProse(t *testing.T) {
	raw := "Here is the receipt:\n```json\n{\"market_name\":\"K-Market\",\"date\":\"2026-05-03\",\"payment_method\":\"cash\",\"items\":[{\"name\":\"Milk\",\"quantity\":12,\"unit_price\":1.00,\"line_total\":12.00}],\"items_total\":12.00,\"vat_total\":0.30,\"total_paid\":12.3}\n```"
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.MarketName != "K-Market" {
		t.Errorf("market: %q", draft.MarketName)
	}
	if draft.TotalCents != 1200 { // sum of lines, VAT not added
		t.Errorf("total: %d", draft.TotalCents)
	}
	if draft.PrintedTotalCents != 1230 {
		t.Errorf("printed total: %d", draft.PrintedTotalCents)
	}
}

func TestParseBillJSON_FillsMissingTotal(t *testing.T) {
	// Without a printed total the computed total (sum of lines) is used, so
	// no mismatch warning appears.
	raw := `{"market_name":"M","date":"2026-05-03","payment_method":"card",
		"items":[{"name":"Milk","quantity":2,"unit_price":5.00,"line_total":10.00}],
		"discount_total":1.00,"vat_total":0.81}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if draft.TotalCents != 1000 {
		t.Errorf("derived total: %d", draft.TotalCents)
	}
	if draft.PrintedTotalCents != 1000 { // no printed total → equals computed, no warning
		t.Errorf("printed fallback: %d", draft.PrintedTotalCents)
	}
	if draft.ItemsSubtotalCents != 1000 {
		t.Errorf("line sum subtotal: %d", draft.ItemsSubtotalCents)
	}
}

func TestParseBillJSON_DepositReturnIsNegative(t *testing.T) {
	// "Leergut" lines are bottle/crate deposit returns — money back. Their
	// negative amounts must survive parsing and reduce the bill total.
	raw := `{"market_name":"REWE","date":"2026-05-03","payment_method":"card",
		"items":[
			{"name":"Milk 1L","quantity":1,"unit_price":1.20,"line_total":1.20},
			{"name":"LEERGUT 8","quantity":1,"unit_price":-1.60,"line_total":-1.60}
		],
		"vat_total":0.19,"total_paid":0.0}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Items) != 2 {
		t.Fatalf("items: %d", len(draft.Items))
	}
	ret := draft.Items[1]
	if !ret.IsReturn {
		t.Error("leergut line not marked as deposit return")
	}
	if ret.LineTotalCents != -100-60 || ret.UnitPriceCents != -160 { // negative kept
		t.Errorf("return line: unit=%d total=%d", ret.UnitPriceCents, ret.LineTotalCents)
	}
	if draft.Items[0].IsReturn {
		t.Error("regular item marked as return")
	}
	if draft.TotalCents != 120-160 { // 1.20 - 1.60 = -0.40
		t.Errorf("total with return: %d (want -40)", draft.TotalCents)
	}
}

func TestNormalizeCardDigits(t *testing.T) {
	cases := map[string]string{
		"4321":      "4321",
		"****4321":  "4321",
		"1234 5678": "5678", // keeps the last 4 only
		"12/34":     "1234",
		"":          "",
	}
	for in, want := range cases {
		if got := normalizeCardDigits(in); got != want {
			t.Errorf("normalizeCardDigits(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseBillJSON_RejectsGarbage(t *testing.T) {
	if _, err := ParseBillJSON("no json here at all"); err == nil {
		t.Fatal("expected error for non-JSON response")
	}
	if _, err := ParseBillJSON("{broken"); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"2026-05-03":           "2026-05-03",
		"03/05/2026":           "2026-05-03",
		"2026-05-03T10:00:00Z": "2026-05-03",
		"not a date":           "",
		"":                     "",
	}
	for in, want := range cases {
		if got := normalizeDate(in); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}
