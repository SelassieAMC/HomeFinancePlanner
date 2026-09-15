package extractor

import (
	"encoding/json"
	"strings"
	"testing"

	"home-finance-planner/backend/internal/domain"
)

func TestParseOffersJSON_PinnedSchema(t *testing.T) {
	raw := `{
		"cannot_search": false,
		"reason": "",
		"products": [
			{
				"product_id": 7,
				"name": "Milk 1L",
				"brand": "Lactona",
				"offers": [
					{"market": " REWE ", "brand": "Lactona", "price": 1.29, "currency": "eur", "is_offer": false},
					{"market": "Lidl", "brand": "Milky", "price": 0.99, "currency": "EUR", "is_offer": true, "note": "flyer week"}
				],
				"note": ""
			},
			{"product_id": 8, "name": "Olive Oil 500ml", "offers": [], "note": "nothing found"}
		]
	}`

	res, err := ParseOffersJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.CannotSearch {
		t.Error("cannot_search should be false")
	}
	if len(res.Products) != 2 {
		t.Fatalf("products: %d (want 2)", len(res.Products))
	}

	first := res.Products[0]
	if first.ProductID != 7 || first.Name != "Milk 1L" || first.Brand != "Lactona" {
		t.Errorf("product header: %+v", first)
	}
	if len(first.Offers) != 2 {
		t.Fatalf("offers: %d (want 2)", len(first.Offers))
	}
	if first.Offers[0].Market != "REWE" { // trimmed
		t.Errorf("market: %q", first.Offers[0].Market)
	}
	if first.Offers[0].PriceCents != 129 {
		t.Errorf("price cents: %d (want 129)", first.Offers[0].PriceCents)
	}
	if first.Offers[0].Currency != "EUR" { // uppercased
		t.Errorf("currency: %q (want EUR)", first.Offers[0].Currency)
	}
	if first.Offers[0].IsOffer {
		t.Error("first offer: is_offer should be false")
	}
	if !first.Offers[1].IsOffer || first.Offers[1].Note != "flyer week" {
		t.Errorf("second offer: %+v", first.Offers[1])
	}
	if res.Products[1].Note != "nothing found" || len(res.Products[1].Offers) != 0 {
		t.Errorf("empty product: %+v", res.Products[1])
	}
}

func TestParseOffersJSON_FencedAndProseWrapped(t *testing.T) {
	fenced := "Sure! Here is the result:\n```json\n{\"products\": [{\"product_id\": 1, \"name\": \"Rice\", \"offers\": [{\"market\": \"Lidl\", \"price\": 2.5, \"currency\": \"EUR\"}]}]}\n```\nHope this helps."
	res, err := ParseOffersJSON(fenced)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Products) != 1 || res.Products[0].Offers[0].PriceCents != 250 {
		t.Errorf("fenced parse: %+v", res)
	}

	prose := "Here you go: {\"products\": [{\"product_id\": 2, \"name\": \"Tea\", \"offers\": [{\"market\": \"REWE\", \"price\": 3.05, \"currency\": \"EUR\"}]}]} — done!"
	res, err = ParseOffersJSON(prose)
	if err != nil {
		t.Fatal(err)
	}
	if res.Products[0].Offers[0].PriceCents != 305 {
		t.Errorf("prose parse: %+v", res)
	}
}

func TestParseOffersJSON_CannotSearchFlag(t *testing.T) {
	res, err := ParseOffersJSON(`{"cannot_search": true, "reason": "no browsing capability", "products": []}`)
	if err != nil {
		t.Fatal(err)
	}
	if !res.CannotSearch || res.Reason != "no browsing capability" {
		t.Errorf("cannot_search parse: %+v", res)
	}
}

// A model answer that omits "offers" must decode to [] — not nil, which
// Go marshals as null and which crashed the frontend result renderer.
func TestParseOffersJSON_MissingOffersAreEmpty(t *testing.T) {
	res, err := ParseOffersJSON(`{"products":[{"product_id":1,"name":"Milk"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Products == nil || len(res.Products) != 1 || res.Products[0].Offers == nil {
		t.Fatalf("nil slices must be normalized to empty: %+v", res)
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "null") {
		t.Errorf("wire output must not contain null: %s", out)
	}
	if !strings.Contains(string(out), `"offers":[]`) {
		t.Errorf("wire output must carry an empty offers array: %s", out)
	}
}

// Pinned-scope searches discriminate stores without a price: those rows must
// survive parsing with their availability, and varieties are kept.
func TestParseOffersJSON_AvailabilityAndVariety(t *testing.T) {
	raw := `{
		"products": [
			{
				"product_id": 1, "name": "Avocado",
				"offers": [
					{"market": "Store1", "price": 0.5, "currency": "EUR", "variety": "Hass"},
					{"market": "Store2", "availability": "not_available"},
					{"market": "Store3", "price": 1.2, "currency": "EUR", "availability": "available", "variety": "XL"},
					{"market": "Store4", "availability": "not_published", "note": "shop online only"},
					{"market": "Store5", "availability": "who knows"},
					{"market": "Store6", "price": 0.9, "currency": "EUR", "availability": "not_available"}
				]
			}
		]
	}`
	res, err := ParseOffersJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := res.Products[0].Offers
	if len(rows) != 5 { // Store5 (unknown availability, no price) is dropped
		t.Fatalf("offers: %d (want 5): %+v", len(rows), rows)
	}
	if rows[0].Variety != "Hass" || rows[0].PriceCents != 50 {
		t.Errorf("row 0: %+v", rows[0])
	}
	if rows[1].Market != "Store2" || rows[1].Availability != domain.OfferNotAvailable || rows[1].PriceCents != 0 {
		t.Errorf("not_available row: %+v", rows[1])
	}
	if rows[2].Availability != domain.OfferAvailable {
		t.Errorf("explicit available row: %+v", rows[2])
	}
	if rows[3].Availability != domain.OfferNotPublished || rows[3].Note != "shop online only" {
		t.Errorf("not_published row: %+v", rows[3])
	}
	// Unknown availability normalizes to available → the priceless row is
	// dropped; a priced row keeps available even with the flag set oddly.
	if rows[4].Market != "Store6" || rows[4].PriceCents != 90 {
		t.Errorf("last kept row: %+v", rows[4])
	}

	// Unavailable rows must never be flagged best/worst.
	out, _ := json.Marshal(res)
	if strings.Contains(string(out), `"best_price":true`) &&
		strings.Contains(string(out), `"price_cents":0,`) {
		t.Errorf("a 0-cent row got a flag: %s", out)
	}
}

func TestParseOffersJSON_RejectsNonJSON(t *testing.T) {
	if _, err := ParseOffersJSON("I cannot search the web, sorry."); err == nil {
		t.Error("expected an error for a response without a JSON object")
	}
}

func TestSearchOffers_CannotSearchFlag(t *testing.T) {
	// A full dispatch path is not testable without a provider; the
	// cannot-search normalization is exercised through the raw response text.
	raw := "I'm sorry, I do not have access to the internet."
	_, parseErr := ParseOffersJSON(raw)
	if parseErr == nil {
		t.Fatal("expected parse failure for prose-only response")
	}
	if !looksLikeCannotSearch(raw) {
		t.Error("refusal phrase should be detected")
	}
}

func TestLooksLikeCannotSearch(t *testing.T) {
	cases := map[string]bool{
		"Unfortunately I cannot browse the web.":              true,
		"I can't search the web right now":                    true,
		"No internet access on this device":                   true,
		"Here are the current prices from the local markets.": false,
		"{\"cannot_search\": false}":                          false,
	}
	for raw, want := range cases {
		if got := looksLikeCannotSearch(raw); got != want {
			t.Errorf("looksLikeCannotSearch(%q) = %v (want %v)", raw, got, want)
		}
	}
}
