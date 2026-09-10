-- Stores: recurring markets/vendors a bill can be linked to. The bill's
-- market name stays a denormalized snapshot (never rewritten on rename).
CREATE TABLE stores (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    location    TEXT    NOT NULL DEFAULT '',
    logo_path   TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Case-insensitive unique name: the find-or-create race in BillService
-- resolves through a unique violation on this index.
CREATE UNIQUE INDEX idx_stores_name ON stores (name COLLATE NOCASE);

-- Backfill: one store per distinct market name (case-insensitive distinct);
-- canonical casing is the smallest spelling seen. Empty names get no store.
INSERT INTO stores (name, description, location, logo_path, created_at, updated_at)
SELECT MIN(TRIM(market_name)), '', '', '', strftime ('%s', 'now'), strftime ('%s', 'now')
FROM bills
WHERE TRIM(market_name) != ''
GROUP BY lower(TRIM(market_name));

ALTER TABLE bills ADD COLUMN store_id INTEGER REFERENCES stores (id) ON DELETE SET NULL;

-- Backfill link: every bill points at its store (NOCASE match), NULL for ''.
UPDATE bills
SET store_id = (SELECT s.id FROM stores s WHERE s.name = TRIM(bills.market_name) COLLATE NOCASE)
WHERE TRIM(market_name) != '';

CREATE INDEX idx_bills_store ON bills (store_id);