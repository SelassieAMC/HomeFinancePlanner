-- Financial goals: named savings targets ("Save €5,000 for vacation") tracked
-- against one account. The goal keeps no money of its own — progress is the
-- live balance of the account it is linked to (contributions are transfers
-- into it), so the balance stays the single source of truth the net-worth
-- calculations already trust. target_date is the optional "by when"
-- milestone; the achieved state is derived (balance >= target), never
-- stored.
CREATE TABLE goals (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    target_cents INTEGER NOT NULL CHECK (target_cents > 0),
    currency     TEXT    NOT NULL,
    account_id   INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    target_date  TEXT    CHECK (target_date IS NULL OR target_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX idx_goals_account ON goals (account_id);