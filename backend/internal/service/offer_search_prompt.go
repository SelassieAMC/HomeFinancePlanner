package service

import (
	"fmt"

	"home-finance-planner/backend/internal/domain"
)

// offersPromptHead pins the task framing and the JSON schema for offer
// searches; the per-search context (product lines, known markets) is appended
// by BuildOffersPrompt.
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
          "price": 1.99,
          "currency": "ISO 4217 code of the price (e.g. \"EUR\")",
          "is_offer": false,
          "note": "promotion details or \"\""
        }
      ]
    }
  ]
}

Rules:
- Search the web for CURRENT retail prices in the product's local market. Never invent prices from memory.
- "is_offer" is true only for a real, currently advertised promotion (flyer/discount), not for the regular shelf price.
- Only include offers whose price you actually found. No offers found → empty "offers" with a short "note".
- If you cannot browse the web or have no search tool, return {"cannot_search": true, "reason": "…"} and no prices — never fabricate offers.
- product_id: echo the id given below for each product.

Product lines and known markets:
`

// BuildOffersPrompt assembles the full search prompt: the pinned schema plus
// the cart's product lines and the user's own market names as context.
func BuildOffersPrompt(products []domain.OfferSearchProduct, storeNames []string) string {
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
	p += "\nKnown local markets (search these first): "
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
	return p
}
