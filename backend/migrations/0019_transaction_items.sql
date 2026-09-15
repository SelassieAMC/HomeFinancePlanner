-- Item lines of a manually entered transaction. Bills keep their lines in
-- bill_items (they carry budget attribution, returns and receipt analysis);
-- manual lines mirror the purchase-relevant subset only. Manual lines never
-- hold negative money: a refund is recorded as an income transaction.
CREATE TABLE transaction_items (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id   INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    product_id       INTEGER REFERENCES products (id) ON DELETE SET NULL,
    name             TEXT    NOT NULL,
    brand            TEXT    NOT NULL DEFAULT '',
    unit             TEXT    NOT NULL DEFAULT '',
    category_id      INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    quantity         REAL    NOT NULL DEFAULT 1 CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL DEFAULT 0 CHECK (unit_price_cents >= 0),
    discount_cents   INTEGER NOT NULL DEFAULT 0 CHECK (discount_cents >= 0),
    line_total_cents INTEGER NOT NULL DEFAULT 0 CHECK (line_total_cents >= 0),
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE INDEX idx_transaction_items_transaction ON transaction_items (transaction_id);
CREATE INDEX idx_transaction_items_product     ON transaction_items (product_id);

-- Manual purchases: the market the items were bought at (NULL for bill
-- transactions, which keep their store on bills.store_id).
ALTER TABLE transactions ADD COLUMN store_id INTEGER REFERENCES stores (id) ON DELETE SET NULL;
CREATE INDEX idx_transactions_store ON transactions (store_id);