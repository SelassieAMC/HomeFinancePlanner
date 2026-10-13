-- Widen the transaction kind CHECK to accept 'transfer' and add the
-- counter_account_id edge (the account the money moved to). A transfer is a
-- single row: account_id holds the source account, counter_account_id the
-- destination (NULL for income/expense, which never have one). Transfers carry
-- no category, store or item lines — they move money, they do not spend it —
-- and every aggregate query (income/expense totals, daily expenses, category
-- spend, analytics) keys on the existing kinds, so they stay excluded by
-- design.
--
-- SQLite cannot alter a CHECK constraint, so the parent table is rebuilt.
-- The runner turns foreign_keys off for this file (the migration carries
-- the pragma marker): with them on, dropping the old parent would cascade
-- into child rows (transaction_items, bills.transaction_id) and destroy data.
-- Children keep referencing the literal name 'transactions'; the new table is
-- created under a placeholder name, filled, and renamed only AFTER the old
-- table is gone, so the children's FK clauses resolve to the rebuilt table.
-- pragma: foreign_keys=off

CREATE TABLE transactions_new (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id         INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    category_id        INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    kind               TEXT    NOT NULL CHECK (kind IN ('income','expense','transfer')),
    amount_cents       INTEGER NOT NULL CHECK (amount_cents > 0),
    description        TEXT    NOT NULL DEFAULT '',
    date               TEXT    NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    currency           TEXT    NOT NULL DEFAULT 'USD',
    bill_id            INTEGER REFERENCES bills (id) ON DELETE SET NULL,
    store_id           INTEGER REFERENCES stores (id) ON DELETE SET NULL,
    counter_account_id INTEGER REFERENCES accounts (id) ON DELETE SET NULL
);

-- Column names, not positions: explicit ids preserve the sequence implicitly
-- (AUTOINCREMENT re-seeds to the max inserted id).
INSERT INTO transactions_new (
    id, account_id, category_id, kind, amount_cents, description, date,
    created_at, updated_at, currency, bill_id, store_id
)
SELECT id, account_id, category_id, kind, amount_cents, description, date,
       created_at, updated_at, currency, bill_id, store_id
FROM transactions;

DROP TABLE transactions;
ALTER TABLE transactions_new RENAME TO transactions;

CREATE INDEX idx_transactions_account  ON transactions (account_id);
CREATE INDEX idx_transactions_date     ON transactions (date);
CREATE INDEX idx_transactions_category ON transactions (category_id);
CREATE INDEX idx_transactions_bill     ON transactions (bill_id);
CREATE INDEX idx_transactions_store    ON transactions (store_id);