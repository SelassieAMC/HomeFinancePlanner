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
	Name         string   `json:"name"`
	StandardName string   `json:"standard_name"`
	GenericName  string   `json:"generic_name"`
	Brand        string   `json:"brand"`
	Unit         string   `json:"unit"`
	UnitValue    *float64 `json:"unit_value"`
	Category     string   `json:"category"`
	Quantity     *float64 `json:"quantity"`
	UnitPrice    *float64 `json:"unit_price"`
	Discount     *float64 `json:"discount"`
	LineTotal    *float64 `json:"line_total"`
}

var jsonFence = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)```")

// fenceStripped returns the ```json fence content when the response is
// fenced, else the trimmed response.
func fenceStripped(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if m := jsonFence.FindStringSubmatch(cleaned); m != nil {
		return m[1]
	}
	return cleaned
}

// balancedObjects returns every balanced {...} region of s in document order.
// Braces are counted only outside JSON strings, so braces inside item names or
// notes don't break the scan — models often append prose after the object
// ("Here is your bill…"), sometimes with stray braces of their own.
func balancedObjects(s string) []string {
	var out []string
	depth, start := 0, -1
	inString, esc := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case esc:
			esc = false
		case inString:
			if c == '\\' {
				esc = true
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
		case c == '{':
			if depth == 0 {
				start = i
			}
			depth++
		case c == '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, s[start:i+1])
					start = -1
				}
			}
		}
	}
	return out
}

// decodeBestObject scans a model response for balanced JSON objects and
// decodes the best one into dest: the first object that decodes with real
// payload wins; an earlier object that decodes but is empty (a model warm-up
// like {"ok": true}) is only a fallback while the search continues. The last
// decode error surfaces when nothing decodes at all.
func decodeBestObject[T any](raw string, dest *T, hasPayload func(*T) bool) error {
	objects := balancedObjects(fenceStripped(raw))
	if len(objects) == 0 {
		return fmt.Errorf("no JSON object found in response")
	}
	var lastErr error
	var fallback *T
	for _, obj := range objects {
		var v T
		if err := json.Unmarshal([]byte(obj), &v); err != nil {
			lastErr = err
			continue
		}
		if hasPayload(&v) {
			*dest = v
			return nil
		}
		if fallback == nil {
			fallback = &v
		}
	}
	if fallback != nil {
		*dest = *fallback // decodable but empty — better than failing the read
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("decode JSON: %w", lastErr)
	}
	return fmt.Errorf("no JSON object found in response")
}

// ParseBillJSON extracts and normalizes the JSON object from a model response.
func ParseBillJSON(raw string) (domain.BillDraft, error) {
	var wire rawWire
	if err := decodeBestObject(raw, &wire, func(w *rawWire) bool {
		return strings.TrimSpace(w.MarketName) != "" || len(w.Items) > 0
	}); err != nil {
		return domain.BillDraft{}, err
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
		// The standardized name is a prompt-contract field: a model (or a
		// user-customized prompt without the rule) may omit it — fall back to
		// the printed text so downstream code can treat it as optional.
		standardName := strings.TrimSpace(it.StandardName)
		if standardName == "" {
			standardName = name
		}
		// The generic (product-family) name is optional the same way, but
		// empty stays empty — unlike the standard name it never falls back
		// to the printed text ("no broader family known" is a real state).
		genericName := strings.TrimSpace(it.GenericName)
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
			StandardName:   standardName,
			GenericName:    genericName,
			Unit:           strings.ToLower(strings.TrimSpace(it.Unit)),
			UnitValue:      unitValueFromWire(it.UnitValue),
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
	// VAT is already included in each item's price, so the total is the sum of
	// the lines minus the bill-level discount — VAT is informational and must
	// never be added again, while the receipt-wide discount (e.g. "10%
	// Rabatt") printed after the lines is a real reduction of the paid amount.
	// The amount printed on the receipt is kept separately so the UI can warn
	// when the computed value does not match it (a sign of a mis-read line).
	draft.ItemsSubtotalCents = toCents(itemsSubtotal)
	draft.TotalCents = draft.ItemsSubtotalCents - draft.DiscountCents
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
	Market       string   `json:"market"`
	Brand        string   `json:"brand"`
	Variety      string   `json:"variety"`
	Price        *float64 `json:"price"`
	Currency     string   `json:"currency"`
	IsOffer      *bool    `json:"is_offer"`
	Availability string   `json:"availability"`
	Note         string   `json:"note"`
}

// normalizeAvailability maps a model-reported availability to the domain
// enum: unknown or empty means available (legacy rows never carried the
// field), so an unpriced row of unknown quality is dropped rather than kept.
func normalizeAvailability(raw string) domain.OfferAvailability {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(domain.OfferNotAvailable):
		return domain.OfferNotAvailable
	case string(domain.OfferNotPublished):
		return domain.OfferNotPublished
	default:
		return domain.OfferAvailable
	}
}

// ParseOffersJSON extracts and normalizes the offers JSON object from a model
// response. Money arrives as decimal numbers in the market's currency and is
// converted to cents.
func ParseOffersJSON(raw string) (domain.OfferResult, error) {
	var wire rawOfferWire
	if err := decodeBestObject(raw, &wire, func(w *rawOfferWire) bool {
		return len(w.Products) > 0 || w.CannotSearch != nil || strings.TrimSpace(w.Reason) != ""
	}); err != nil {
		return domain.OfferResult{}, err
	}

	res := domain.OfferResult{
		CannotSearch: wire.CannotSearch != nil && *wire.CannotSearch,
		Reason:       strings.TrimSpace(wire.Reason),
		Products:     []domain.OfferProductResult{}, // never null on the wire
	}
	for _, p := range wire.Products {
		out := domain.OfferProductResult{
			Name:   strings.TrimSpace(p.Name),
			Brand:  strings.TrimSpace(p.Brand),
			Note:   strings.TrimSpace(p.Note),
			Offers: []domain.OfferRow{}, // never null on the wire
		}
		if p.ProductID != nil {
			out.ProductID = *p.ProductID
		}
		for _, o := range p.Offers {
			market := strings.TrimSpace(o.Market)
			if market == "" {
				continue // an offer without a market cannot be discriminated
			}
			availability := normalizeAvailability(o.Availability)
			// Priceless rows are kept only as explicit "no price here"
			// discrimination (store does not carry / does not publish it).
			if o.Price == nil {
				if !availability.Unavailable() {
					continue
				}
				out.Offers = append(out.Offers, domain.OfferRow{
					Market:       market,
					Brand:        strings.TrimSpace(o.Brand),
					Variety:      strings.TrimSpace(o.Variety),
					Availability: availability,
					Note:         strings.TrimSpace(o.Note),
				})
				continue
			}
			currency := strings.ToUpper(strings.TrimSpace(o.Currency))
			out.Offers = append(out.Offers, domain.OfferRow{
				Market:       market,
				Brand:        strings.TrimSpace(o.Brand),
				Variety:      strings.TrimSpace(o.Variety),
				PriceCents:   toCents(*o.Price),
				Currency:     currency,
				IsOffer:      o.IsOffer != nil && *o.IsOffer,
				Availability: availability,
				Note:         strings.TrimSpace(o.Note),
			})
		}
		res.Products = append(res.Products, out)
	}
	return res, nil
}

// unitValueFromWire keeps a real, positive size magnitude ("500" for
// "500ml") and rejects anything a model may emit in its place: absent, 0,
// negative, NaN/Inf — those mean "not printed" and stay nil.
func unitValueFromWire(v *float64) *float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) || *v <= 0 {
		return nil
	}
	return v
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
