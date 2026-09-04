-- bills: scanned receipts awaiting review or already accepted
CREATE TABLE bills (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    market_name          TEXT    NOT NULL DEFAULT '',
    date                 TEXT    NOT NULL DEFAULT '',
    payment_method       TEXT    NOT NULL DEFAULT '',
    currency             TEXT    NOT NULL DEFAULT 'USD',
    items_subtotal_cents INTEGER NOT NULL DEFAULT 0,
    discount_cents       INTEGER NOT NULL DEFAULT 0,
    vat_cents            INTEGER NOT NULL DEFAULT 0,
    total_cents          INTEGER NOT NULL DEFAULT 0,
    status               TEXT    NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending','draft','accepted','discarded')),
    image_path           TEXT    NOT NULL DEFAULT '',
    extracted_by         TEXT    NOT NULL DEFAULT '',
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL
);

CREATE INDEX idx_bills_status ON bills (status);
CREATE INDEX idx_bills_date   ON bills (date);

-- bill_items: individual articles printed on a bill
CREATE TABLE bill_items (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    bill_id          INTEGER NOT NULL REFERENCES bills (id) ON DELETE CASCADE,
    name             TEXT    NOT NULL,
    quantity         REAL    NOT NULL DEFAULT 1,
    unit_price_cents INTEGER NOT NULL DEFAULT 0,
    discount_cents   INTEGER NOT NULL DEFAULT 0,
    line_total_cents INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_bill_items_bill ON bill_items (bill_id);

-- settings: app configuration key/value store (AI provider configs live here,
-- with API keys AES-GCM-encrypted by the crypto package before storage)
CREATE TABLE settings (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);