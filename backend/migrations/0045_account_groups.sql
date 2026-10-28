-- Account groups (Phase 4): folder-style presentation grouping of accounts.
-- Zero money semantics — an account's derived balance folds identically
-- whether it belongs to a group or not. Deleting a group just ungroups its
-- accounts; never cascades into accounts or transactions.
CREATE TABLE account_groups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

ALTER TABLE accounts ADD COLUMN group_id INTEGER REFERENCES account_groups (id) ON DELETE SET NULL;