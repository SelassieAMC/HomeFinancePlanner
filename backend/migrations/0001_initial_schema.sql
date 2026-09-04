-- accounts: user-held financial accounts
-- Timestamps are unix seconds (UTC) for portable, driver-agnostic scanning.
CREATE TABLE accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    type          TEXT    NOT NULL CHECK (type IN ('checking','savings','credit','cash','other')),
    currency      TEXT    NOT NULL DEFAULT 'USD',
    balance_cents INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX idx_accounts_name ON accounts (name);

-- categories: transaction groupings
CREATE TABLE categories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

-- transactions: movements of money on accounts
CREATE TABLE transactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id   INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    category_id  INTEGER REFERENCES categories (id) ON DELETE SET NULL,
    kind         TEXT    NOT NULL CHECK (kind IN ('income','expense')),
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    description  TEXT    NOT NULL DEFAULT '',
    date         TEXT    NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_transactions_account  ON transactions (account_id);
CREATE INDEX idx_transactions_date     ON transactions (date);
CREATE INDEX idx_transactions_category ON transactions (category_id);

-- budgets: monthly caps per category; month is 'YYYY-MM'
CREATE TABLE budgets (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id  INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    month        TEXT    NOT NULL CHECK (month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]'),
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    UNIQUE (category_id, month)
);