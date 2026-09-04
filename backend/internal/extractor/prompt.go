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
      "category": "one of the fixed storage categories, written EXACTLY as listed: Produce, Dairy & Eggs, Meats & Seafood, Deli & Ready-to-Eat, Chilled Condiments, Grains & Carbs, Canned Goods, Oils & Vinegars, Spices & Baking, Breakfast & Spreads, Snacks & Treats, Beverages, Root Vegetables, Frozen Proteins, Frozen Fruits & Veggies, Frozen Ready Meals, Frozen Breads, Paper Goods, Food Wrap & Storage, Cleaning & Dish",
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
- classify every item into the fixed category list by how the product is stored: frozen products go to a Frozen category, fridge products to Produce/Dairy & Eggs/Meats & Seafood/Deli & Ready-to-Eat/Chilled Condiments, room-temperature groceries to the Pantry categories, and non-food items to Paper Goods, Food Wrap & Storage or Cleaning & Dish. Bread is "Frozen Breads" only when the product is sold frozen.
- unit_price is the printed price per unit (VAT/IVA already included — read the printed value verbatim); discount is the per-line market discount if printed (0 otherwise); line_total is what the line costs after its discount, VAT included. Discounts are informational only — never change the printed unit price.
- Deposit/bottle returns (lines like "Leergut", "Pfand" returns, empty bottles) are money BACK: read their amounts as NEGATIVE numbers exactly as printed (e.g. line_total -1.50 for an 8¢-bottle crate return). Do not drop those lines and do not flip their sign.
- discount_total is any global/market-level discount printed on the receipt (0 if none). It is informational only.
- vat_total is the total VAT/IVA amount printed on the receipt (0 if not shown). It is informational only — VAT is already included in the item prices, so it is never added to the total.
- total_paid is the final amount EXACTLY as printed at the bottom of the receipt — the amount actually paid. Read it verbatim; never compute or derive it from the items or VAT.
- card_last_digits: only the digits printed on the receipt (masked card numbers like ****4321 give "4321"); "" when not paid by card or no digits printed.
- If a value is genuinely not printed, use 0 (or 1 for quantity) or "" for text. Do not invent values.
- Respond with ONLY the JSON object.`
