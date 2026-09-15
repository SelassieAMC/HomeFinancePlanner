package service

import (
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// offersPromptHead pins the task framing and the JSON schema for offer
// searches; the per-search context (product lines, scope, known markets) is
// appended by BuildOffersPrompt.
const offersPromptHead = `You are a grocery price research engine. For each product below, find its current prices in the local markets listed at the end. Use your web-search tool when you have one — search current offers, flyers and shop prices for the product's country/region.

Return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (prices are decimal numbers in the market's currency, e.g. 1.99 — never cents, never strings):
{
  "cannot_search": false,
  "reason": "",
  "products": [
    {
      "product_id": 1,
      "name": "product name as requested",
      "brand": "brand requested for the search, if any",
      "note": "short note when nothing was found for this product, else \"\"",
      "offers": [
        {
          "market": "market/store name where the offer was found (e.g. REWE, Lidl, Carrefour)",
          "brand": "the brand actually found for this price",
          "variety": "the exact product name/variety the market sells (e.g. \"Hass avocado\", \"XL\"); \"\" when identical to the requested name",
          "price": 1.99,
          "currency": "ISO 4217 code of the price (e.g. \"EUR\")",
          "is_offer": false,
          "availability": "available",
          "note": "promotion details or \"\""
        }
      ]
    }
  ]
}

Rules:
- Search the web for CURRENT retail prices in the product's local market. Never invent prices from memory.
- "availability" is "available" when you found a price. Use "not_available" when a market in scope does not carry the product (currently or seasonally) and "not_published" when the market exists but publishes no price for it online. Rows with availability other than "available" must NOT carry a price or currency.
- "is_offer" is true only for a real, currently advertised promotion (flyer/discount), not for the regular shelf price.
- Only include offers whose price you actually found. No offers found → empty "offers" with a short "note".
- If you cannot browse the web or have no search tool, return {"cannot_search": true, "reason": "…"} and no prices — never fabricate offers.
- product_id: echo the id given below for each product.

Product lines:
`

// BuildOffersPrompt assembles the full search prompt: the pinned schema plus
// the cart's product lines, the search scope (pinned markets or the user's
// own market names as hints) and the name-match mode.
func BuildOffersPrompt(products []domain.OfferSearchProduct, storeNames, pinnedStores []string, nameMatch domain.OfferNameMatch) string {
	p := offersPromptHead
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
