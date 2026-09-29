package extractor

import (
	"testing"
)

func TestParseBillJSON_PinnedSchema(t *testing.T) {
	raw := `{
		"market_name": " FreshMart ",
		"date": "05/03/2026",
		"currency": " eur ",
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
	if draft.Currency != "EUR" { // trimmed + uppercased
		t.Errorf("currency: %q (want EUR)", draft.Currency)
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
	if draft.Currency != "" { // absent currency stays empty (service defaults it)
		t.Errorf("currency: %q (want empty when not printed)", draft.Currency)
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

func TestParseBillJSON_StandardNameFromModel(t *testing.T) {
	// standard_name is part of the pinned schema: the raw printed text stays
	// authoritative on the line, the readable form rides alongside it.
	raw := `{"market_name":"REWE","date":"2026-05-03","payment_method":"card",
		"items":[{"name":"WHL MLK 1L","standard_name":"Whole Milk 1L","quantity":1,"unit_price":1.20,"line_total":1.20}]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Items) != 1 {
		t.Fatalf("items: %d (want 1)", len(draft.Items))
	}
	if draft.Items[0].Name != "WHL MLK 1L" {
		t.Errorf("raw name: %q (want the printed text verbatim)", draft.Items[0].Name)
	}
	if draft.Items[0].StandardName != "Whole Milk 1L" {
		t.Errorf("standard name: %q (want %q)", draft.Items[0].StandardName, "Whole Milk 1L")
	}
}

func TestParseBillJSON_StandardNameFallsBackToRawName(t *testing.T) {
	// A model (or a user-customized prompt without the rule) may omit or blank
	// the field — downstream code treats it as optional and sees the printed
	// text instead.
	raw := `{"market_name":"M","date":"2026-05-03","payment_method":"cash",
		"items":[
			{"name":"TOMATOS","quantity":1,"unit_price":2.00,"line_total":2.00},
			{"name":"BREAD","standard_name":"","quantity":1,"unit_price":1.50,"line_total":1.50},
			{"name":"EGGS","standard_name":"   ","quantity":1,"unit_price":2.50,"line_total":2.50}
		]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Items) != 3 {
		t.Fatalf("items: %d (want 3)", len(draft.Items))
	}
	for _, it := range draft.Items {
		if it.StandardName != it.Name {
			t.Errorf("standard name for %q: %q (want fallback to the raw name)", it.Name, it.StandardName)
		}
	}
}

func TestParseBillJSON_GenericName(t *testing.T) {
	// generic_name is part of the pinned schema, but unlike standard_name it
	// never falls back to the raw text — empty means "no broader family
	// known" (a user-customized prompt without the rule yields empty).
	raw := `{"market_name":"REWE","date":"2026-05-03","payment_method":"card",
		"items":[
			{"name":"POTATO MINIONS 450G","standard_name":"Potato Minions 450g","generic_name":"Frozen Shaped Potatoes","quantity":1,"unit_price":2.99,"line_total":2.99},
			{"name":"BANANE","standard_name":"Bananas","quantity":1,"unit_price":1.19,"line_total":1.19},
			{"name":"BREAD","standard_name":"Bread","generic_name":"  ","quantity":1,"unit_price":1.50,"line_total":1.50}
		]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Items) != 3 {
		t.Fatalf("items: %d (want 3)", len(draft.Items))
	}
	if got := draft.Items[0].GenericName; got != "Frozen Shaped Potatoes" {
		t.Errorf("generic name: %q (want %q)", got, "Frozen Shaped Potatoes")
	}
	for _, it := range draft.Items[1:] {
		if it.GenericName != "" {
			t.Errorf("generic name for %q: %q (want empty, never the raw name)", it.Name, it.GenericName)
		}
	}
}

func TestParseNormalizationJSON_GenericName(t *testing.T) {
	// The generic family name is optional in the answer schema: present →
	// carried, missing → empty (custom prompts keep working). Entries are
	// still dropped only on an empty name or standard name.
	raw := `{"items":[
		{"name":"POTATO MINIONS 450G","standard_name":"Potato Minions 450g","generic_name":"Frozen Shaped Potatoes"},
		{"name":"MILCH 1L","standard_name":"Milk 1L"},
		{"name":"BANANE","standard_name":"Bananas","generic_name":"  "},
		{"name":"","standard_name":"Nameless"},
		{"name":"NO STANDARD","standard_name":" "}
	]}`
	items, err := ParseNormalizationJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items: %d (want 3 — the last two are dropped)", len(items))
	}
	if items[0].GenericName != "Frozen Shaped Potatoes" {
		t.Errorf("generic name: %q (want %q)", items[0].GenericName, "Frozen Shaped Potatoes")
	}
	if items[1].GenericName != "" {
		t.Errorf("missing generic must decode as empty, got %q", items[1].GenericName)
	}
	if items[2].GenericName != "" {
		t.Errorf("blank generic must trim to empty, got %q", items[2].GenericName)
	}
}

func TestParseBillJSON_StandardNameDepositReturnFallback(t *testing.T) {
	// Deposit-return lines get no special-casing at parse level: the generic
	// fallback applies, so a "Leergut" line without the field carries its raw
	// printed text as the standardized name.
	raw := `{"market_name":"REWE","date":"2026-05-03","payment_method":"card",
		"items":[{"name":"LEERGUT 8","quantity":1,"unit_price":-1.60,"line_total":-1.60}]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Items) != 1 {
		t.Fatalf("items: %d (want 1)", len(draft.Items))
	}
	ret := draft.Items[0]
	if !ret.IsReturn {
		t.Error("leergut line not marked as deposit return")
	}
	if ret.StandardName != "LEERGUT 8" {
		t.Errorf("standard name: %q (want fallback to %q)", ret.StandardName, "LEERGUT 8")
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
	// An object that never closes is not a decodable candidate either.
	if _, err := ParseBillJSON(`{"market_name":"REWE","items":[`); err == nil {
		t.Fatal("expected error for unterminated JSON")
	}
}

// Models often append prose after the object ("Here is your bill…"). The
// balanced scan must stop at the object's closing brace instead of slicing
// to the last brace in the response — the old slice failed with
// "invalid character 'H' after top-level value".
func TestParseBillJSON_TrailingProse(t *testing.T) {
	raw := `{"market_name":"REWE","date":"2026-05-03","items":[{"name":"MILCH","quantity":1,"unit_price":1.20,"line_total":1.20}]}` +
		"\nHere is the extracted bill. Let me know if you need anything else!"
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatalf("trailing prose broke the read: %v", err)
	}
	if draft.MarketName != "REWE" || len(draft.Items) != 1 {
		t.Errorf("draft = %+v, want the parsed bill", draft)
	}
}

// The appended prose may carry braces of its own — still no mis-slice.
func TestParseBillJSON_TrailingProseWithBraces(t *testing.T) {
	raw := `{"market_name":"REWE","items":[{"name":"MILCH","quantity":1,"unit_price":1.20,"line_total":1.20}]} Hope this helps { :^) } stay healthy!}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatalf("prose braces broke the read: %v", err)
	}
	if draft.MarketName != "REWE" {
		t.Errorf("market = %q, want REWE", draft.MarketName)
	}
}

// A decodable warm-up object before the real one ({"ok": true} … {draft}) is
// skipped in favor of the first object with actual payload.
func TestParseBillJSON_SkipsEmptyWarmupObject(t *testing.T) {
	raw := `{"ok":true} And here is the draft: {"market_name":"Kaufland","items":[{"name":"Bread","quantity":1,"unit_price":2.00,"line_total":2.00}]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatalf("warm-up object broke the read: %v", err)
	}
	if draft.MarketName != "Kaufland" || len(draft.Items) != 1 {
		t.Errorf("draft = %+v, want the second (payload) object", draft)
	}

	// …but when the empty object is all there is, it still decodes (an empty
	// draft the user can edit beats a failed scan).
	draft, err = ParseBillJSON(`{"ok":true}`)
	if err != nil {
		t.Fatalf("empty object should still decode: %v", err)
	}
	if draft.MarketName != "" || len(draft.Items) != 0 {
		t.Errorf("draft = %+v, want the empty payload as-is", draft)
	}
}

// Braces inside string values (item names, notes) don't break the balance
// count.
func TestParseBillJSON_BracesInsideStrings(t *testing.T) {
	raw := `{"market_name":"M{ark}et","items":[{"name":"Milk {bio} \"x\"","quantity":1,"unit_price":1.00,"line_total":1.00}]}`
	draft, err := ParseBillJSON(raw)
	if err != nil {
		t.Fatalf("braces inside strings broke the read: %v", err)
	}
	if draft.MarketName != "M{ark}et" || draft.Items[0].Name != `Milk {bio} "x"` {
		t.Errorf("draft = %+v, want string values verbatim", draft)
	}
}

// The offers parser gets the same trailing-prose tolerance.
func TestParseOffersJSON_TrailingProse(t *testing.T) {
	raw := `{"products":[{"product_id":1,"name":"Milk","offers":[{"market":"REWE","price":1.29,"currency":"EUR"}]}]}` +
		" Hope this helps you shop smartly!"
	res, err := ParseOffersJSON(raw)
	if err != nil {
		t.Fatalf("trailing prose broke the offer parse: %v", err)
	}
	if len(res.Products) != 1 || res.Products[0].Offers[0].PriceCents != 129 {
		t.Errorf("result = %+v, want the parsed offers", res)
	}
}

// And so does the product-normalization parser.
func TestParseNormalizationJSON_TrailingProse(t *testing.T) {
	raw := `{"items":[{"name":"WHL MLK 1L","standard_name":"Whole Milk 1L","generic_name":"Milk"}]}` +
		" Here are the normalized names!"
	items, err := ParseNormalizationJSON(raw)
	if err != nil {
		t.Fatalf("trailing prose broke the normalization parse: %v", err)
	}
	if len(items) != 1 || items[0].StandardName != "Whole Milk 1L" || items[0].GenericName != "Milk" {
		t.Errorf("items = %+v, want the parsed mappings", items)
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
