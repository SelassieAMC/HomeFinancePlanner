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

// ParseBillJSON extracts and normalizes the JSON object from a model response.
func ParseBillJSON(raw string) (domain.BillDraft, error) {
	cleaned := strings.TrimSpace(raw)
	if m := jsonFence.FindStringSubmatch(cleaned); m != nil {
		cleaned = m[1]
	}
	// Some models wrap the object in prose; keep the outermost braces.
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start < 0 || end <= start {
		return domain.BillDraft{}, fmt.Errorf("no JSON object found in response")
	}

	var wire rawWire
	if err := json.Unmarshal([]byte(cleaned[start:end+1]), &wire); err != nil {
		return domain.BillDraft{}, fmt.Errorf("decode JSON: %w", err)
	}

	draft := domain.BillDraft{
		MarketName:     strings.TrimSpace(wire.MarketName),
		Date:           normalizeDate(wire.Date),
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
