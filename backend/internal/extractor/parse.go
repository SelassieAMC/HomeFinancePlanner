package extractor

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"home-finance-planner/backend/internal/domain"
)

// rawWire mirrors the JSON schema pinned in prompt.go. Money arrives as
// decimal numbers in the receipt currency and is converted to cents here.
type rawWire struct {
	MarketName     string    `json:"market_name"`
	Date           string    `json:"date"`
	Currency       string    `json:"currency"`
	PaymentMethod  string    `json:"payment_method"`
	CardLastDigits string    `json:"card_last_digits"`
	Items          []rawItem `json:"items"`
	DiscountTotal  *float64  `json:"discount_total"`
	VATTotal       *float64  `json:"vat_total"`
	TotalPaid      *float64  `json:"total_paid"`
}

type rawItem struct {
	Name      string   `json:"name"`
	Brand     string   `json:"brand"`
	Unit      string   `json:"unit"`
	Category  string   `json:"category"`
	Quantity  *float64 `json:"quantity"`
	UnitPrice *float64 `json:"unit_price"`
	Discount  *float64 `json:"discount"`
	LineTotal *float64 `json:"line_total"`
}

var jsonFence = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// extractJSONObject isolates the JSON object from a model response: a
// ```json fence wins, otherwise the outermost braces survive (prose-wrapped
// output from grounded searches).
func extractJSONObject(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	if m := jsonFence.FindStringSubmatch(cleaned); m != nil {
		cleaned = m[1]
	}
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start < 0 || end <= start {
		return "", fmt.Errorf("no JSON object found in response")
	}
	return cleaned[start : end+1], nil
}

// ParseBillJSON extracts and normalizes the JSON object from a model response.
func ParseBillJSON(raw string) (domain.BillDraft, error) {
	cleaned, err := extractJSONObject(raw)
	if err != nil {
		return domain.BillDraft{}, err
	}

	var wire rawWire
	if err := json.Unmarshal([]byte(cleaned), &wire); err != nil {
		return domain.BillDraft{}, fmt.Errorf("decode JSON: %w", err)
	}

	draft := domain.BillDraft{
		MarketName:     strings.TrimSpace(wire.MarketName),
		Date:           normalizeDate(wire.Date),
		Currency:       strings.ToUpper(strings.TrimSpace(wire.Currency)),
		PaymentMethod:  strings.ToLower(strings.TrimSpace(wire.PaymentMethod)),
		CardLastDigits: normalizeCardDigits(wire.CardLastDigits),
	}

	itemsSubtotal := 0.0
	for _, it := range wire.Items {
		name := strings.TrimSpace(it.Name)
		if name == "" {
			continue
		}
		qty := 1.0
		if it.Quantity != nil && *it.Quantity > 0 {
			qty = *it.Quantity
		}
		var unit, disc, line float64
		if it.UnitPrice != nil {
			unit = *it.UnitPrice
		}
		if it.Discount != nil {
			disc = *it.Discount
		}
		if it.LineTotal != nil {
			line = *it.LineTotal
		} else {
			line = qty*unit - disc // fallback when the receipt prints no line total
		}
		itemsSubtotal += line
		draft.Items = append(draft.Items, domain.BillItemDraft{
			Name:           name,
			Brand:          strings.TrimSpace(it.Brand),
			Unit:           strings.ToLower(strings.TrimSpace(it.Unit)),
			CategoryName:   strings.ToLower(strings.TrimSpace(it.Category)),
			Quantity:       qty,
			UnitPriceCents: toCents(unit),
			DiscountCents:  toCents(disc),
			LineTotalCents: toCents(line),
			IsReturn:       isDepositReturn(name),
		})
	}

	if wire.DiscountTotal != nil {
		draft.DiscountCents = toCents(*wire.DiscountTotal)
	}
	if wire.VATTotal != nil {
		draft.VATCents = toCents(*wire.VATTotal)
	}
	// VAT is already included in each item's price, so the total is simply the
	// sum of the lines — VAT is informational and must never be added again.
	// The amount printed on the receipt is kept separately so the UI can warn
	// when the computed value does not match it (a sign of a mis-read line).
	draft.ItemsSubtotalCents = toCents(itemsSubtotal)
	draft.TotalCents = draft.ItemsSubtotalCents
	if wire.TotalPaid != nil {
		draft.PrintedTotalCents = toCents(*wire.TotalPaid)
	} else {
		draft.PrintedTotalCents = draft.TotalCents // nothing printed → no warning
	}
	return draft, nil
}

var nonDigits = regexp.MustCompile(`\D`)

// rawOfferWire mirrors the JSON schema pinned in offersPrompt. Price is a
// decimal number in the offer's currency and is converted to cents here.
type rawOfferWire struct {
	CannotSearch *bool         `json:"cannot_search"`
	Reason       string        `json:"reason"`
	Products     []rawProductW `json:"products"`
}

type rawProductW struct {
	ProductID *int64      `json:"product_id"`
	Name      string      `json:"name"`
	Brand     string      `json:"brand"`
	Note      string      `json:"note"`
	Offers    []rawOfferW `json:"offers"`
}

type rawOfferW struct {
	Market   string   `json:"market"`
	Brand    string   `json:"brand"`
	Price    *float64 `json:"price"`
	Currency string   `json:"currency"`
	IsOffer  *bool    `json:"is_offer"`
	Note     string   `json:"note"`
}

// ParseOffersJSON extracts and normalizes the offers JSON object from a model
// response. Money arrives as decimal numbers in the market's currency and is
// converted to cents.
func ParseOffersJSON(raw string) (domain.OfferResult, error) {
	cleaned, err := extractJSONObject(raw)
	if err != nil {
		return domain.OfferResult{}, err
	}

	var wire rawOfferWire
	if err := json.Unmarshal([]byte(cleaned), &wire); err != nil {
		return domain.OfferResult{}, fmt.Errorf("decode JSON: %w", err)
	}

	res := domain.OfferResult{
		CannotSearch: wire.CannotSearch != nil && *wire.CannotSearch,
		Reason:       strings.TrimSpace(wire.Reason),
	}
	for _, p := range wire.Products {
		out := domain.OfferProductResult{
			Name:  strings.TrimSpace(p.Name),
			Brand: strings.TrimSpace(p.Brand),
			Note:  strings.TrimSpace(p.Note),
		}
		if p.ProductID != nil {
			out.ProductID = *p.ProductID
		}
		for _, o := range p.Offers {
			market := strings.TrimSpace(o.Market)
			if o.Price == nil || market == "" {
				continue // an offer without a market or price is useless
			}
			currency := strings.ToUpper(strings.TrimSpace(o.Currency))
			out.Offers = append(out.Offers, domain.OfferRow{
				Market:     market,
				Brand:      strings.TrimSpace(o.Brand),
				PriceCents: toCents(*o.Price),
				Currency:   currency,
				IsOffer:    o.IsOffer != nil && *o.IsOffer,
				Note:       strings.TrimSpace(o.Note),
			})
		}
		res.Products = append(res.Products, out)
	}
	return res, nil
}

// isDepositReturn reports whether an article line is a bottle/crate deposit
// return (e.g. German "Leergut") — money coming back, so its amount may be
// negative and it reduces the bill total instead of adding to it.
func isDepositReturn(name string) bool {
	return strings.Contains(strings.ToLower(name), "leergut")
}

// normalizeCardDigits keeps only digits and caps the value at the last 4.
func normalizeCardDigits(raw string) string {
	digits := nonDigits.ReplaceAllString(strings.TrimSpace(raw), "")
	if len(digits) > 4 {
		digits = digits[len(digits)-4:]
	}
	return digits
}

// toCents converts a decimal money value to integer cents (rounded).
func toCents(v float64) int64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return int64(math.Round(v * 100))
}

var dateFormats = []string{
	"2006-01-02",
	"02/01/2006", // DD/MM/YYYY
	"01/02/2006", // MM/DD/YYYY
	"02-01-2006",
	"01-02-2006",
	"2006/01/02",
	"02.01.2006",
	"2006-01-02T15:04:05Z07:00",
}

// normalizeDate converts common receipt date formats to YYYY-MM-DD, or "".
func normalizeDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.Format("2006-01-02")
		}
	}
	// Last resort: YYYYMMDD.
	if t, err := time.Parse("20060102", raw); err == nil {
		return t.Format("2006-01-02")
	}
	return ""
}
