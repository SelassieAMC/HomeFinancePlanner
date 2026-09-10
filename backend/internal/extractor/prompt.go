package extractor

// extractionPrompt instructs vision models to emit strict JSON for a receipt.
// Field names are pinned so parse.go can rely on them across connectors.
const extractionPrompt = `You are a receipt-parsing engine. Read the receipt (image or PDF) and return ONE JSON object and nothing else — no explanations, no markdown fences.

Schema (all money values are decimal numbers in the receipt's currency, e.g. 12.34 — never cents, never strings):
{
  "market_name": "store or market name from the header",
  "date": "YYYY-MM-DD",
  "payment_method": "cash | card | credit | debit | transfer | voucher | other (pick the closest; use what the receipt shows)",
  "card_last_digits": "last 4 digits of the card printed on the receipt (e.g. \"4321\"), or \"\" for cash/other",
  "items": [
    {
      "name": "article name as printed",
      "brand": "product brand if recognizable, else \"\"",
      "category": "one of the fixed product categories, written EXACTLY as listed: Vegetables, Fruits, Dairy & Eggs, Cheese, Meats, Seafood, Deli & Ready-to-Eat, Chilled Condiments, "Pasta, Rice & Grains", Canned & Jarred, Oils & Vinegars, Spices & Baking, Breakfast & Spreads, Sweets & Chocolate, Salty Snacks, Nuts & Dried Fruits, Root Vegetables, Water & Iced Tea, Cola & Soda, Juice, Coffee & Tea, Milk Drinks & Alternatives, Beer, Wine, Spirits & Liqueurs, Frozen Vegetables, Frozen Fruits, Frozen Meat & Fish, Frozen Ready Meals, Frozen Breads, Paper Goods, Food Wrap & Storage, Cleaning & Dish, Deposit & Returns",
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
- "items" lists every article line, one entry per article, in receipt order.
- quantity defaults to 1 when not printed; use decimals for weights (0.532 kg) and set "unit" to the printed measure (kg, g, l, ml, pcs, …).
- classify every item into the fixed category list, choosing the MOST SPECIFIC category (the list is fine-grained so spending can be analyzed per product family):
  * Drinks are never a generic bucket: water/iced tea → "Water & Iced Tea", cola and other sodas/energy drinks → "Cola & Soda", juices and nectars → "Juice", coffee/tea products → "Coffee & Tea", plant milks and drinking yogurt → "Milk Drinks & Alternatives", beer (incl. non-alcoholic and radler) → "Beer", wine and sparkling wine → "Wine", hard alcohol → "Spirits & Liqueurs". Plain milk itself is "Dairy & Eggs".
  * Fresh produce splits by type: vegetables and fresh herbs → "Vegetables"; fruit → "Fruits". Potatoes, onions and garlic stay "Root Vegetables".
  * "Meats" is for raw meat, "Seafood" for fish and shellfish — never mix them; pre-packed sliced charcuterie is "Deli & Ready-to-Eat". Cheeses are "Cheese", other chilled dairy "Dairy & Eggs".
  * Pantry: dry pasta/rice/grains/flour → "Pasta Rice & Grains"; canned or jarred food → "Canned & Jarred"; chocolate, candy and cookies → "Sweets & Chocolate"; chips/crackers/pretzels → "Salty Snacks"; nuts, seeds and dried fruit → "Nuts & Dried Fruits".
  * Frozen products always go to a Frozen category by type (Frozen Vegetables, Frozen Fruits, Frozen Meat & Fish, Frozen Ready Meals, Frozen Breads). Bread is "Frozen Breads" only when sold frozen.
  * Non-food items go to Paper Goods, Food Wrap & Storage or Cleaning & Dish.
  * Deposit lines ("Pfand", bottle/crate deposits) and bottle return lines ("Leergut", empty bottles) always go to "Deposit & Returns" — never to the drink family.
- unit_price is the printed price per unit (VAT/IVA already included — read the printed value verbatim); discount is the per-line market discount if printed (0 otherwise); line_total is what the line costs after its discount, VAT included. Discounts are informational only — never change the printed unit price.
- Deposit/bottle returns ("Leergut" and other refund lines in "Deposit & Returns") are money BACK: read their amounts as NEGATIVE numbers exactly as printed (e.g. line_total -1.50 for an 8¢-bottle crate return). A "Pfand" deposit CHARGE is money spent: keep it POSITIVE, also under "Deposit & Returns". Do not drop deposit lines and do not flip their signs.
- discount_total is any global/market-level discount printed on the receipt (0 if none). It is informational only.
- vat_total is the total VAT/IVA amount printed on the receipt (0 if not shown). It is informational only — VAT is already included in the item prices, so it is never added to the total.
- total_paid is the final amount EXACTLY as printed at the bottom of the receipt — the amount actually paid. Read it verbatim; never compute or derive it from the items or VAT.
- card_last_digits: only the digits printed on the receipt (masked card numbers like ****4321 give "4321"); "" when not paid by card or no digits printed.
- If a value is genuinely not printed, use 0 (or 1 for quantity) or "" for text. Do not invent values.
- Respond with ONLY the JSON object.`
