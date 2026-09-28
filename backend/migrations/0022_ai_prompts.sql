-- AI prompts: the instruction texts sent to AI connectors, managed from the
-- UI (settings/prompts) instead of being hard-coded. key is the stable lookup
-- the processes resolve by ('bill_extraction', 'offer_search'); the seeded
-- content matches the built-in defaults in internal/service/prompt_defaults.go
-- exactly (guarded by a repository test). An empty or deleted row is safe --
-- resolve falls back to the built-in default.
CREATE TABLE ai_prompts (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    key         TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_ai_prompts_key ON ai_prompts (key COLLATE NOCASE);

INSERT INTO ai_prompts (key, name, description, content, created_at, updated_at) VALUES
('bill_extraction', 'Bill extraction', 'Used by AI bill scanning: turns a receipt image or PDF into the structured bill draft (market, date, items, categories, totals). {{categories}} is replaced at run time with the live product-category list.', 'You are a receipt-parsing engine. Read the receipt (image or PDF) and return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (all money values are decimal numbers in the receipt''s currency, e.g. 12.34 — never cents, never strings):
{
  "market_name": "store or market name from the header",
  "date": "YYYY-MM-DD",
  "currency": "ISO 4217 code of the currency the amounts are printed in (e.g. \"EUR\", \"USD\", \"GBP\")",
  "payment_method": "cash | card | credit | debit | transfer | voucher | other (pick the closest; use what the receipt shows)",
  "card_last_digits": "last 4 digits of the card printed on the receipt (e.g. \"4321\"), or \"\" for cash/other",
  "items": [
    {
      "name": "article name as printed",
      "brand": "product brand if recognizable, else \"\"",
      "category": "one of the fixed product categories, written EXACTLY as listed: {{categories}}",
      "unit": "measure unit printed with the quantity (kg, g, l, ml, pcs, …), or \"\" for plain counts",
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
- currency: detect the ISO 4217 code of the receipt''s currency from the symbol or name printed next to any amount (€ → EUR, $ → USD, £ → GBP, zł → PLN, CHF → CHF, "kr" with a Swedish market → SEK, etc.). If no symbol is printed anywhere, use the currency of the country the market is in. Return "" ONLY when the currency is genuinely not determinable — never invent a code.
- "items" lists every article line, one entry per article, in receipt order.
- quantity defaults to 1 when not printed; use decimals for weights (0.532 kg) and set "unit" to the printed measure (kg, g, l, ml, pcs, …).
- classify every item into the fixed category list, choosing the MOST SPECIFIC category (the list is fine-grained so spending can be analyzed per product family):
  * Drinks are never a generic bucket: water/iced tea → "Water & Iced Tea", cola and other sodas/energy drinks → "Cola & Soda", juices and nectars → "Juice", coffee/tea products → "Coffee & Tea", plant milks and drinking yogurt → "Milk Drinks & Alternatives", beer (incl. non-alcoholic and radler) → "Beer", wine and sparkling wine → "Wine", hard alcohol → "Spirits & Liqueurs". Plain milk itself is "Dairy & Eggs".
  * Fresh produce splits by type: vegetables and fresh herbs → "Vegetables"; fruit → "Fruits". Potatoes, onions and garlic stay "Root Vegetables".
  * "Meats" is for raw meat, "Seafood" for fish and shellfish — never mix them; pre-packed sliced charcuterie is "Deli & Ready-to-Eat". Cheeses are "Cheese", other chilled dairy "Dairy & Eggs".
  * Pantry: dry pasta/rice/grains/flour → "Pasta Rice & Grains"; canned or jarred food → "Canned & Jarred"; chocolate, candy and cookies → "Sweets & Chocolate"; chips/crackers/pretzels → "Salty Snacks"; nuts, seeds and dried fruit → "Nuts & Dried Fruits".
  * Frozen products always go to a Frozen category by type (Frozen Vegetables, Frozen Fruits, Frozen Meat & Fish, Frozen Ready Meals, Frozen Breads). Bread is "Frozen Breads" only when sold frozen.
  * Non-food items go to Paper Goods, Food Wrap & Storage or Cleaning & Dish.
  * Non-grocery receipts classify by store type: gas stations (petrol/diesel/E5/E10/Super/AdBlue at the pump → "Fuel & Gasoline"; engine/transmission oil, coolant, screenwash → "Car Oils & Fluids"; wiper blades, bulbs, filters, car wax → "Car Parts & Care"; food/drinks on the same receipt → their normal food categories). Drugstores/pharmacies: medicines and remedies → "Medicines"; vitamins, minerals, protein powder → "Vitamins & Supplements"; bandages/gauze/disinfectant → "First Aid"; makeup, skincare, perfume → "Cosmetics"; shampoo, soap, deodorant, toothpaste, shaving → "Hair & Body Care".
  * Pet food, litter and pet accessories → "Pet Supplies". Diapers, wipes, baby food and formula → "Baby Care"; toys, board games and video games (any age) → "Toys & Games". Screws, tools, light bulbs, glue, small electrical → "Hardware & Tools"; plants, seeds, soil, garden tools → "Garden & Outdoor". Chargers, cables, headphones, household batteries → "Electronics & Accessories"; pens, paper, printer ink → "Stationery & Office"; books, magazines, DVDs → "Books & Media". Clothes, shoes and accessories → "Clothing & Footwear".
  * Deposit lines ("Pfand", bottle/crate deposits) and bottle return lines ("Leergut", empty bottles) always go to "Deposit & Returns" — never to the drink family.
- unit_price is the printed price per unit (VAT/IVA already included — read the printed value verbatim); discount is the per-line market discount if printed (0 otherwise); line_total is what the line costs after its discount, VAT included. Discounts are informational only — never change the printed unit price.
- Deposit/bottle returns ("Leergut" and other refund lines in "Deposit & Returns") are money BACK: read their amounts as NEGATIVE numbers exactly as printed (e.g. line_total -1.50 for an 8¢-bottle crate return). A "Pfand" deposit CHARGE is money spent: keep it POSITIVE, also under "Deposit & Returns". Do not drop deposit lines and do not flip their signs.
- discount_total is any global/market-level discount printed on the receipt (0 if none). It is informational only.
- vat_total is the total VAT/IVA amount printed on the receipt (0 if not shown). It is informational only — VAT is already included in the item prices, so it is never added to the total.
- total_paid is the final amount EXACTLY as printed at the bottom of the receipt — the amount actually paid. Read it verbatim; never compute or derive it from the items or VAT.
- card_last_digits: only the digits printed on the receipt (masked card numbers like ****4321 give "4321"); "" when not paid by card or no digits printed.
- If a value is genuinely not printed, use 0 (or 1 for quantity) or "" for text. Do not invent values.
- Respond with ONLY the JSON object.', strftime('%s', 'now'), strftime('%s', 'now')),
('offer_search', 'Offer search', 'Used by the purchase-cart offer search: asks the AI for current market prices of the cart products. The product lines, market scope and name-match mode are appended automatically.', 'You are a grocery price research engine. For each product below, find its current prices in the local markets listed at the end. Use your web-search tool when you have one — search current offers, flyers and shop prices for the product''s country/region.

Return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (prices are decimal numbers in the market''s currency, e.g. 1.99 — never cents, never strings):
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
- Search the web for CURRENT retail prices in the product''s local market. Never invent prices from memory.
- "availability" is "available" when you found a price. Use "not_available" when a market in scope does not carry the product (currently or seasonally) and "not_published" when the market exists but publishes no price for it online. Rows with availability other than "available" must NOT carry a price or currency.
- "is_offer" is true only for a real, currently advertised promotion (flyer/discount), not for the regular shelf price.
- Only include offers whose price you actually found. No offers found → empty "offers" with a short "note".
- If you cannot browse the web or have no search tool, return {"cannot_search": true, "reason": "…"} and no prices — never fabricate offers.
- product_id: echo the id given below for each product.

Product lines:
', strftime('%s', 'now'), strftime('%s', 'now'));
