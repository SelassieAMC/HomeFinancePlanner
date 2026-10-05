package service

import "home-finance-planner/backend/internal/domain"

// defaultBillExtractionPrompt is the fallback used when the ai_prompts row
// for key 'bill_extraction' is missing or empty. Field names are pinned so
// parse.go can rely on them across connectors. {{categories}} is expanded
// from the live product-kind category list at resolve time.
const defaultBillExtractionPrompt = `You are a receipt-parsing engine. You receive the files of ONE receipt: a single image or PDF, or several photos that are consecutive parts of one long paper receipt (part 1 = top of the receipt, last part = bottom). Read ALL files together as ONE receipt and return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (all money values are decimal numbers in the receipt's currency, e.g. 12.34 — never cents, never strings):
{
  "market_name": "store or market name from the header",
  "date": "YYYY-MM-DD",
  "currency": "ISO 4217 code of the currency the amounts are printed in (e.g. \"EUR\", \"USD\", \"GBP\")",
  "payment_method": "cash | card | credit | debit | transfer | voucher | other (pick the closest; use what the receipt shows)",
  "card_last_digits": "last 4 digits of the card printed on the receipt (e.g. \"4321\"), or \"\" for cash/other",
  "items": [
    {
      "name": "article name exactly as printed on the receipt",
      "standard_name": "standardized, human-readable name for this article (see rules)",
      "generic_name": "generic product-family name for this article (see rules)",
      "brand": "product brand if recognizable, else \"\"",
      "category": "one of the fixed product categories, written EXACTLY as listed: {{categories}}",
      "unit": "measure unit printed with the quantity (kg, g, l, ml, pcs, …), or \"\" for plain counts",
      "unit_value": 500,
      "quantity": 1.0,
      "unit_price": 2.5,
      "discount": 0.0,
      "line_total": 2.5
    }
  ],
  "discount_total": 0.0,
  "vat_total": 0.0,
  "total_paid": 0.0
}

Rules:
- The files may show several photos of the SAME receipt. Treat them as ONE purchase, never as separate receipts: merge all parts into ONE bill.
- "items" covers every article line from ALL parts in receipt order: part 1's lines first, then part 2's, and so on. Where two photos overlap, the same line appears in both — include it exactly once.
- market_name, date, payment_method and card_last_digits are usually printed on the FIRST part; read them from whichever part shows them.
- total_paid (and any printed discount_total / vat_total) come from the LAST part — that is where the receipt ends.
- currency: detect the ISO 4217 code of the receipt's currency from the symbol or name printed next to any amount (€ → EUR, $ → USD, £ → GBP, zł → PLN, CHF → CHF, "kr" with a Swedish market → SEK, etc.). If no symbol is printed anywhere, use the currency of the country the market is in. Return "" ONLY when the currency is genuinely not determinable — never invent a code.
- "items" lists every article line, one entry per article, in receipt order.
- quantity defaults to 1 when not printed; use decimals for weights (0.532 kg) and set "unit" to the printed measure (kg, g, l, ml, pcs, …).
- unit_value is the NUMERIC magnitude of the printed size as a number only — "500" for a 500ml bottle (unit "ml"), "1.5" for a 1.5l pack, "500" for a 500g pack (unit "g"). It pairs with "unit" so prices can be calculated per unit. Omit it or use 0 when the size is not printed; never include the unit text inside it. For plain counts (pcs) or when no size is printed, omit it or use 0.
- classify every item into the fixed category list, choosing the MOST SPECIFIC category (the list is fine-grained so spending can be analyzed per product family):
  * Drinks are never a generic bucket: water/iced tea → "Water & Iced Tea", cola and other sodas/energy drinks → "Cola & Soda", juices and nectars → "Juice", coffee/tea products → "Coffee & Tea", plant milks and drinking yogurt → "Milk Drinks & Alternatives", beer (incl. non-alcoholic and radler) → "Beer", wine and sparkling wine → "Wine", hard alcohol → "Spirits & Liqueurs". Plain milk itself is "Dairy & Eggs".
  * Fresh produce splits by type: vegetables and fresh herbs → "Vegetables"; fruit → "Fruits". Potatoes, onions and garlic stay "Root Vegetables".
  * "Meats" is for raw meat, "Seafood" for fish and shellfish — never mix them; pre-packed sliced charcuterie is "Deli & Ready-to-Eat". Cheeses are "Cheese", other chilled dairy "Dairy & Eggs".
  * Pantry: dry pasta/rice/grains/flour → "Pasta Rice & Grains"; canned or jarred food → "Canned & Jarred"; chocolate, candy and cookies → "Sweets & Chocolate"; chips/crackers/pretzels → "Salty Snacks"; nuts, seeds and dried fruit → "Nuts & Dried Fruits".
  * Frozen products always go to a Frozen category by type (Frozen Vegetables, Frozen Fruits, Frozen Meat & Fish, Frozen Ready Meals, Frozen Breads). Bread is "Frozen Breads" only when sold frozen.
  * Non-food items go to Paper Goods, Food Wrap & Storage or Cleaning & Dish.
  * Non-grocery receipts classify by store type: gas stations (petrol/diesel/E5/E10/Super/AdBlue at the pump → "Fuel & Gasoline"; engine/transmission oil, coolant, screenwash → "Car Oils & Fluids"; wiper blades, bulbs, filters, car wax → "Car Parts & Care"; food/drinks on the same receipt → their normal food categories). Drugstores/pharmacies: medicines and remedies → "Medicines"; vitamins, minerals, protein powder → "Vitamins & Supplements"; bandages/gauze/disinfectant → "First Aid"; makeup, skincare, perfume → "Cosmetics"; shampoo, soap, deodorant, toothpaste, shaving → "Hair & Body Care".
  * Pet food, litter and pet accessories → "Pet Supplies". Diapers, wipes, baby food and formula → "Baby Care"; toys, board games and video games (any age) → "Toys & Games". Screws, tools, light bulbs, glue, small electrical → "Hardware & Tools"; plants, seeds, soil, garden tools → "Garden & Outdoor". Chargers, cables, headphones, household batteries → "Electronics & Accessories"; pens, paper, printer ink → "Stationery & Office"; books, magazines, DVDs → "Books & Media". Clothes, shoes and accessories → "Clothing & Footwear".
  * Deposit lines ("Pfand", bottle/crate deposits) and bottle return lines ("Leergut", empty bottles) always go to "Deposit & Returns" — never to the drink family. Deposit charges ("Pfand", "MEHRWEG", "EINWEG", "Bottle deposit") are refundable bottle/crate deposits, not purchases: they belong under "Deposit & Returns" just like the returns, and their printed names are never normalized into a product name.
- standard_name: based on the product in "name" and its category, provide a standardized, human-readable name for this product. Expand receipt abbreviations and store shorthand ("WHL MLK 1L" → "Whole Milk 1L", "TOMATOS" → "Tomatoes"), use Title Case, never put the brand into the name, and keep the size/quantity qualifiers printed on the line ("1L", "500G"). It must be the SAME article, only readable — never invent a different product. When the printed name is already plain, echo it unchanged. Deposit and bottle-return lines ("Deposit & Returns") echo the printed name unchanged.
- generic_name: the generic product-family name this article belongs to, in Title Case, without brand, size, quantity or variety qualifiers ("POTATO MINIONS 450G" → "Frozen Shaped Potatoes", "Cola Zero 1.5L" → "Cola"). It groups the same kind of product across brands and sizes and is usually shorter than "standard_name". Use the most specific family that is still generic — plain unbranded produce with no size variants may be the product itself ("Tomatoes"). When no broader family exists, repeat the standard_name. Deposit and bottle-return lines ("Deposit & Returns") echo the printed name unchanged.
- BOTH "standard_name" AND "generic_name" must ALWAYS be in English, regardless of the receipt's language — translate "Kartoffel Minions 450G" to "Potato Minions 450g" / "Frozen Shaped Potatoes", "Milch 1L" to "Milk 1L" / "Fresh Milk".
- unit_price is the printed price per unit (VAT/IVA already included — read the printed value verbatim); discount is the per-line market discount if printed (0 otherwise); line_total is what the line costs after its discount, VAT included. Discounts are informational only — never change the printed unit price.
- Deposit/bottle returns ("Leergut" and other refund lines in "Deposit & Returns") are money BACK: read their amounts as NEGATIVE numbers exactly as printed (e.g. line_total -1.50 for an 8¢-bottle crate return). A "Pfand" deposit CHARGE is money spent: keep it POSITIVE, also under "Deposit & Returns". Do not drop deposit lines and do not flip their signs.
- A bare "GRATIS" (or "Free", free-gift) line is a marker, not an article: never emit the marker itself as a standalone item. The free product it refers to is ONE line — its real printed name and real product category with unit_price 0 and line_total 0 — or the already-printed 0.00 product line of that product, never a duplicate copy of it.
- discount_total is any global/market-level discount printed on the receipt — a store-wide rebate or coupon applied after the article lines (e.g. "10% Rabatt", Payback/paper coupons). 0 if none. It is subtracted from the item sum: the article lines at their printed prices minus discount_total should reconcile with total_paid.
- vat_total is the total VAT/IVA amount printed on the receipt (0 if not shown). It is informational only — VAT is already included in the item prices, so it is never added to the total.
- total_paid is the final amount EXACTLY as printed at the bottom of the receipt — the amount actually paid. Read it verbatim; never compute or derive it from the items or VAT.
- card_last_digits: only the digits printed on the receipt (masked card numbers like ****4321 give "4321"); "" when not paid by card or no digits printed.
- If a value is genuinely not printed, use 0 (or 1 for quantity) or "" for text. Do not invent values.
- Respond with ONLY the JSON object.`

// defaultOffersPromptHead is the fallback head for key 'offer_search'. It
// pins the task framing and the JSON schema for offer searches; the
// per-search context (product lines, scope, known markets) is appended by
// BuildOffersPrompt.
const defaultOffersPromptHead = `You are a grocery price research engine. For each product below, find its current prices in the local markets listed at the end. Use your web-search tool when you have one — search current offers, flyers and shop prices for the product's country/region.

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

// defaultProductNormalizationPrompt is the fallback for key
// 'product_normalization'. The raw names to standardize are appended as a
// JSON array; the model returns one JSON object mapping each input name to
// its standardized form. Used by the "analyze existing products" job.
const defaultProductNormalizationPrompt = `You are a product-name normalizer. You receive a JSON array of raw product names as printed on receipts or typed by hand. Return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema:
{
  "items": [
    {
      "name": "the input name, echoed verbatim",
      "standard_name": "standardized, human-readable name for this product",
      "generic_name": "generic product-family name for this product (see rules)"
    }
  ]
}

Rules:
- Based on each raw product name, provide a standardized, human-readable name. Expand receipt abbreviations and store shorthand ("WHL MLK 1L" → "Whole Milk 1L", "TOMATOS" → "Tomatoes"), use Title Case, never put the brand into the name, and keep the size/quantity qualifiers printed with the name ("1L", "500G").
- It must be the SAME article, only readable — never invent a different product. When the name is already plain, echo it unchanged.
- generic_name: the generic product-family name the product belongs to, in Title Case, without brand, size or variety qualifiers ("POTATO MINIONS 450G" → "Frozen Shaped Potatoes"). Use the most specific family that is still generic; when no broader family exists, repeat the "standard_name".
- BOTH "standard_name" AND "generic_name" must ALWAYS be in English, regardless of the input's language — translate German/Spanish/etc. product texts ("Kartoffel Minions" → "Potato Minions", "Milch" → "Milk", "Gefrorene Gemüse" → "Frozen Vegetables").
- Cover every input name exactly once, echoing each "name" verbatim so the caller can match the answers back.
- Deposit/refund/free-marker lines — "Pfand", "Leergut", "Mehrweg", "Einweg", "Bottle deposit", "Gratis" — are deposit artifacts, not products: echo such a name unchanged for BOTH names and never turn it into a product-family name.
- If a name is too ambiguous to standardize confidently, echo it unchanged.
- Respond with ONLY the JSON object.

Raw product names:
`

// defaultUnitValueBackfillPrompt is the fallback for key
// 'unit_value_backfill', the one-time River job resolving printed size
// magnitudes (migration 0027's unit_value) for stored products and lines
// whose raw names the size parser cannot read. The raw names to resolve are
// appended as a JSON array plus the machine format hints.
const defaultUnitValueBackfillPrompt = `You are a grocery product-size resolver. You receive a JSON array of raw product names as printed on receipts, stored in the catalogue or typed in purchase lines. For each name, derive the printed package/portion size the shop prints next to the article — the numeric magnitude and its measure. Return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema:
{
  "items": [
    {
      "name": "the input name, echoed verbatim",
      "unit": "the measure the size pairs with, one of: kg, g, l, ml, pcs",
      "unit_value": 500
    }
  ]
}

Rules:
- "unit_value" is the NUMERIC magnitude of the printed size, as a number only — "500" for a 500ml bottle (unit "ml"), "1.5" for a 1.5l pack (unit "l"), "500" for a 500g pack (unit "g"). Never include the unit text inside the number.
- Convert odd printed units to the canonical measure: "0,5 l" → unit "l", unit_value 0.5; "33 cl" → unit "ml", unit_value 330; "1.000 g" → unit "g", unit_value 1000; "2 x 500 g" → unit "g", unit_value 500 (the size of ONE item).
- Multi-packs and counted articles ("Eggs 10 pcs", "6-pack yogurt") use unit "pcs" with the number of pieces ONE item contains.
- Never guess: when the name carries no printed size, return unit "" and unit_value 0 for it — a missing answer is always better than an invented one.
- Cover every input name exactly once, echoing each "name" verbatim so the caller can match the answers back.
- Respond with ONLY the JSON object.
`

// defaultPrompt returns the built-in template content for a prompt key —
// the fallback when the ai_prompts row is missing or empty. Custom keys
// have no built-in; an empty result means "nothing to fall back to".
func defaultPrompt(key string) string {
	switch key {
	case domain.PromptKeyBillExtraction:
		return defaultBillExtractionPrompt
	case domain.PromptKeyOfferSearch:
		return defaultOffersPromptHead
	case domain.PromptKeyProductNormalization:
		return defaultProductNormalizationPrompt
	case domain.PromptKeyUnitValueBackfill:
		return defaultUnitValueBackfillPrompt
	default:
		return ""
	}
}
