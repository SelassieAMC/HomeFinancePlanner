-- 0003: scan adjustments — card digits, per-item brand + category.
-- Additive columns only; existing rows get zero-value defaults.

ALTER TABLE bills ADD COLUMN card_last_digits TEXT NOT NULL DEFAULT '';

ALTER TABLE bill_items ADD COLUMN brand TEXT NOT NULL DEFAULT '';

-- unit of measure as printed next to the quantity (kg, g, l, ml, pcs, …).
ALTER TABLE bill_items ADD COLUMN unit TEXT NOT NULL DEFAULT '';

ALTER TABLE bill_items
  ADD COLUMN category_id INTEGER REFERENCES categories (id) ON DELETE SET NULL;

-- Accounts can carry the last card digits so scanned bills can be matched
-- ("card •1234") or auto-matched on accept.
ALTER TABLE accounts ADD COLUMN card_last_digits TEXT NOT NULL DEFAULT '';