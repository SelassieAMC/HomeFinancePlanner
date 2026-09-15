-- Products: the catalogue of everything ever bought. Rows are find-or-created
-- from bill item names when a bill is confirmed; the user only edits them,
-- never creates or deletes them. Product edits (name, unit, category)
-- propagate to the linked bill_items, unlike stores, whose names are snapshots.
CREATE TABLE products (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    brand       TEXT    NOT NULL DEFAULT '',
    unit        TEXT    NOT NULL DEFAULT '',
    category_id INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    description TEXT    NOT NULL DEFAULT '',
    image_path  TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Case-insensitive unique name: the find-or-create race in BillService
-- resolves through a unique violation on this index (mirrors idx_stores_name).
CREATE UNIQUE INDEX idx_products_name ON products (name COLLATE NOCASE);

ALTER TABLE bill_items ADD COLUMN product_id INTEGER REFERENCES products (id) ON DELETE SET NULL;
CREATE INDEX idx_bill_items_product ON bill_items (product_id);

-- Backfill, step 1: one product per case-insensitive distinct (trimmed) item
-- name. Deposit returns ("Leergut") are excluded — they are money back, not
-- purchases. Canonical casing is MIN(name): the alphabetically smallest
-- spelling seen (mirrors the stores backfill in 0010).
INSERT INTO products (name, brand, unit, category_id, description, image_path, created_at, updated_at)
SELECT MIN(TRIM(bi.name)), '', '', NULL, '', '', strftime ('%s', 'now'), strftime ('%s', 'now')
FROM bill_items bi
WHERE TRIM(bi.name) != '' AND bi.is_return = 0
GROUP BY lower(TRIM(bi.name));

-- Backfill, step 2: link every non-return item to its product (NOCASE match,
-- as in 0010). Later steps key off product_id, so this must run first.
UPDATE bill_items
SET product_id = (SELECT p.id FROM products p WHERE p.name = TRIM(bill_items.name) COLLATE NOCASE)
WHERE TRIM(name) != '' AND is_return = 0;

-- Backfill, step 3: enrich with the most frequent non-empty value, tie-broken
-- deterministically. Scalar subqueries may carry GROUP BY/ORDER BY/LIMIT.
-- COALESCE keeps the NOT NULL columns populated (no rows → NULL → '').
UPDATE products SET unit = COALESCE((
    SELECT lower(TRIM(bi.unit)) FROM bill_items bi
    WHERE bi.product_id = products.id AND TRIM(bi.unit) != ''
    GROUP BY lower(TRIM(bi.unit))
    ORDER BY COUNT (*) DESC, lower(TRIM(bi.unit)) ASC
    LIMIT 1
), '');

UPDATE products SET brand = COALESCE((
    SELECT TRIM(bi.brand) FROM bill_items bi
    WHERE bi.product_id = products.id AND TRIM(bi.brand) != ''
    GROUP BY lower(TRIM(bi.brand))
    ORDER BY COUNT (*) DESC, TRIM(bi.brand) ASC
    LIMIT 1
), '');

-- Nullable, so an empty group (no categorised item) stays NULL.
UPDATE products SET category_id = (
    SELECT bi.category_id FROM bill_items bi
    WHERE bi.product_id = products.id AND bi.category_id IS NOT NULL
    GROUP BY bi.category_id
    ORDER BY COUNT (*) DESC, bi.category_id ASC
    LIMIT 1
);