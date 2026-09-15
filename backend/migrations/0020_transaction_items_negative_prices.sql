-- Manual transaction item lines may now hold negative money: deposit returns
-- ("Leergut"/Pfand) and lines filed under a category with allows_negative are
-- money back and reduce the transaction's items total. SQLite cannot drop a
-- CHECK constraint, so the table is rebuilt without the non-negative checks
-- on unit_price_cents and line_total_cents; the rule itself lives in the
-- service (mirrors the bill flow), discount_cents stays non-negative.
ALTER TABLE transaction_items RENAME TO transaction_items_old;
CREATE TABLE transaction_items (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id   INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    product_id       INTEGER REFERENCES products (id) ON DELETE SET NULL,
    name             TEXT    NOT NULL,
    brand            TEXT    NOT NULL DEFAULT '',
    unit             TEXT    NOT NULL DEFAULT '',
    category_id      INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    quantity         REAL    NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL DEFAULT 0,
    discount_cents   INTEGER NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    line_total_cents INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);
INSERT INTO transaction_items (
    id, transaction_id, product_id, name, brand, unit, category_id,
    quantity, unit_price_cents, discount_cents, line_total_cents, created_at, updated_at
)
SELECT id, transaction_id, product_id, name, brand, unit, category_id,
       quantity, unit_price_cents, discount_cents, line_total_cents, created_at, updated_at
FROM transaction_items_old;
DROP TABLE transaction_items_old;

CREATE INDEX idx_transaction_items_transaction ON transaction_items (transaction_id);
CREATE INDEX idx_transaction_items_product     ON transaction_items (product_id);