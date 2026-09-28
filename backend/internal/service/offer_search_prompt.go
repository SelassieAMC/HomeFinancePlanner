package service

import (
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// BuildOffersPrompt assembles the full search prompt: the managed head (the
// task framing and the pinned JSON schema, resolved from ai_prompts with the
// built-in default as fallback) plus the cart's product lines, the search
// scope (pinned markets or the user's own market names as hints) and the
// name-match mode.
func BuildOffersPrompt(head string, products []domain.OfferSearchProduct, storeNames, pinnedStores []string, nameMatch domain.OfferNameMatch) string {
	p := head
	for _, prod := range products {
		p += fmt.Sprintf("- {\"product_id\": %d, \"name\": %q", prod.ProductID, prod.Name)
		if prod.Brand != "" {
			p += fmt.Sprintf(", \"brand_hint\": %q", prod.Brand)
		}
		if prod.Unit != "" {
			p += fmt.Sprintf(", \"unit\": %q", prod.Unit)
		}
		if prod.Quantity > 0 {
			p += fmt.Sprintf(", \"quantity_to_buy\": %g", prod.Quantity)
		}
		if prod.LastPriceCents != nil && prod.Currency != "" {
			p += fmt.Sprintf(", \"last_known_price\": %q", fmt.Sprintf("%.2f %s", float64(*prod.LastPriceCents)/100, prod.Currency))
		}
		p += "}\n"
	}

	if len(pinnedStores) > 0 {
		p += "\nSearch scope — search ONLY these markets: "
		for i, s := range pinnedStores {
			if i > 0 {
				p += ", "
			}
			p += s
		}
		p += "\nFor EVERY product above report one offer entry per listed market. Never omit a market: a market that does not carry the product gets availability \"not_available\" (no price), a market that exists but publishes no price for it online gets \"not_published\" (no price). Do not add markets outside this list.\n"
	} else {
		p += "\nKnown local markets (search these first; other local markets are allowed): "
		if len(storeNames) == 0 {
			p += "(none recorded)"
		}
		for i, s := range storeNames {
			if i > 0 {
				p += ", "
			}
			p += s
		}
		p += "\n"
	}

	switch nameMatch {
	case domain.OfferNameLoose:
		p += "Name match: LOOSE — include every variety and similar naming of the product (e.g. avocado → Hass, XL, ready-to-eat; yogurt → Greek, natural). Report what each market actually sells and set \"variety\" accordingly.\n"
	default: // strict (also for empty — the default)
		p += "Name match: STRICT — only report the product exactly as named above; do not substitute varieties or similar products (omit \"variety\" or repeat the exact name).\n"
	}
	return p
}
