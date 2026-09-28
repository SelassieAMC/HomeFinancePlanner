-- Product name mappings: the normalization memory ("canonical product"
-- layer). One row per distinct raw text as printed on a receipt or typed
-- manually; several raw texts may share the same standardized name. Product
-- rows keep their raw names -- this table only remembers how raw texts
-- resolve onto a standardized, human-readable name (+ category).
CREATE TABLE product_name_mappings (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    raw_name      TEXT    NOT NULL,
    standard_name TEXT    NOT NULL,
    category_id   INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    source        TEXT    NOT NULL DEFAULT 'ai' CHECK (source IN ('ai','user','manual')),
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_product_name_mappings_raw ON product_name_mappings (raw_name COLLATE NOCASE);

-- Single-row status of the user-triggered "analyze existing products" job.
CREATE TABLE product_normalization_jobs (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    status          TEXT    NOT NULL DEFAULT 'idle' CHECK (status IN ('idle','running','done','failed')),
    total_names     INTEGER NOT NULL DEFAULT 0,
    processed_names INTEGER NOT NULL DEFAULT 0,
    mapped_names    INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
INSERT INTO product_normalization_jobs (id, status, created_at, updated_at)
VALUES (1, 'idle', strftime('%s', 'now'), strftime('%s', 'now'));

-- Seed the managed prompt for the normalization job; the content matches
-- the built-in default in internal/service/prompt_defaults.go exactly
-- (guarded by a repository test). The raw-name array is appended in code.
INSERT INTO ai_prompts (key, name, description, content, created_at, updated_at) VALUES
('product_normalization', 'Product normalization', 'Used by the "analyze existing products" job: asks the AI to standardize raw product names into human-readable form. The raw-name list is appended automatically.', 'You are a product-name normalizer. You receive a JSON array of raw product names as printed on receipts or typed by hand. Return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema:
{
  "items": [
    {
      "name": "the input name, echoed verbatim",
      "standard_name": "standardized, human-readable name for this product"
    }
  ]
}

Rules:
- Based on each raw product name, provide a standardized, human-readable name. Expand receipt abbreviations and store shorthand ("WHL MLK 1L" → "Whole Milk 1L", "TOMATOS" → "Tomatoes"), use Title Case, never put the brand into the name, and keep the size/quantity qualifiers printed with the name ("1L", "500G").
- It must be the SAME article, only readable — never invent a different product. When the name is already plain, echo it unchanged.
- Cover every input name exactly once, echoing each "name" verbatim so the caller can match the answers back.
- If a name is too ambiguous to standardize confidently, echo it unchanged.
- Respond with ONLY the JSON object.

Raw product names:
', strftime('%s', 'now'), strftime('%s', 'now'));

-- Refresh the bill_extraction seed to the new default (adds the
-- standard_name field) -- ONLY while the row still carries the previous
-- built-in default byte-for-byte, so user-customized prompts survive.
UPDATE ai_prompts
SET content = 'You are a receipt-parsing engine. Read the receipt (image or PDF) and return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (all money values are decimal numbers in the receipt''s currency, e.g. 12.34 — never cents, never strings):
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
- standard_name: based on the product in "name" and its category, provide a standardized, human-readable name for this product. Expand receipt abbreviations and store shorthand ("WHL MLK 1L" → "Whole Milk 1L", "TOMATOS" → "Tomatoes"), use Title Case, never put the brand into the name, and keep the size/quantity qualifiers printed on the line ("1L", "500G"). It must be the SAME article, only readable — never invent a different product. When the printed name is already plain, echo it unchanged. Deposit and bottle-return lines ("Deposit & Returns") echo the printed name unchanged.
- unit_price is the printed price per unit (VAT/IVA already included — read the printed value verbatim); discount is the per-line market discount if printed (0 otherwise); line_total is what the line costs after its discount, VAT included. Discounts are informational only — never change the printed unit price.
- Deposit/bottle returns ("Leergut" and other refund lines in "Deposit & Returns") are money BACK: read their amounts as NEGATIVE numbers exactly as printed (e.g. line_total -1.50 for an 8¢-bottle crate return). A "Pfand" deposit CHARGE is money spent: keep it POSITIVE, also under "Deposit & Returns". Do not drop deposit lines and do not flip their signs.
- discount_total is any global/market-level discount printed on the receipt (0 if none). It is informational only.
- vat_total is the total VAT/IVA amount printed on the receipt (0 if not shown). It is informational only — VAT is already included in the item prices, so it is never added to the total.
- total_paid is the final amount EXACTLY as printed at the bottom of the receipt — the amount actually paid. Read it verbatim; never compute or derive it from the items or VAT.
- card_last_digits: only the digits printed on the receipt (masked card numbers like ****4321 give "4321"); "" when not paid by card or no digits printed.
- If a value is genuinely not printed, use 0 (or 1 for quantity) or "" for text. Do not invent values.
- Respond with ONLY the JSON object.',
    updated_at = strftime('%s', 'now')
WHERE key = 'bill_extraction'
  AND content = 'You are a receipt-parsing engine. Read the receipt (image or PDF) and return ONE JSON object and nothing else — no explanations, no markdown fences.

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
- Respond with ONLY the JSON object.';

