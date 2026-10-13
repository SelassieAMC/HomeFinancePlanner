-- Bags: named allocation envelopes ("sinking funds") — money set aside inside
-- ONE real account for a named purpose. A bag holds no money of its own (no
-- bank account per bag, no virtual cadence): its balance is derived as
-- Σ allocations − tagged spend, all-time. Allocations are plan-level ledger
-- rows (fund / rebalance / withdraw), NOT transactions — they enter no
-- aggregate by construction, the same discipline transfers already follow.
-- Spending out of a bag is never copied into the ledger: expenses are tagged
-- (bag_id on the transaction / bill, with the bill item line-level override
-- winning, mirroring the budget attribution COALESCE from 0006) and computed
-- live, so every later edit of a transaction keeps the attribution honest.
-- Funding recurrence deliberately lives in the (future) scheduler, not here.
--
-- Deletion is a plain delete — no history is kept (decision 2026-10-13):
-- allocations cascade with the bag, tagged transactions/bills fall back to
-- untagged spend via ON DELETE SET NULL and read as normal expenses. The
-- service refuses deleting a bag an active schedule still targets (a guard
-- that only bites once the scheduler exists).
CREATE TABLE bags (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,
    account_id   INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    target_cents INTEGER CHECK (target_cents IS NULL OR target_cents > 0),
    status       TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
    closed_at    INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

-- Names are unique among OPEN bags of the account (budget precedent): a
-- closed bag frees its name.
CREATE UNIQUE INDEX bags_one_open_name_per_account
    ON bags (account_id, name) WHERE status = 'open';

-- Every reservation is a row with a signed amount. Withdraw exists so a
-- closed/cut bag releases money back to "unbagged"; rebalance is the pair leg
-- of a bag→bag move (related_allocation_id links the two halves), adjust is
-- the rare manual correction of a stale balance.
CREATE TABLE bag_allocations (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    bag_id                INTEGER NOT NULL REFERENCES bags (id) ON DELETE CASCADE,
    kind                  TEXT    NOT NULL CHECK (kind IN ('fund','rebalance','withdraw','adjust')),
    amount_cents          INTEGER NOT NULL CHECK (amount_cents <> 0),
    note                  TEXT    NOT NULL DEFAULT '',
    date                  TEXT    NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    related_allocation_id INTEGER REFERENCES bag_allocations (id) ON DELETE SET NULL,
    created_at            INTEGER NOT NULL,
    updated_at            INTEGER NOT NULL
);

CREATE INDEX idx_bag_allocations_bag ON bag_allocations (bag_id, date);

-- Spend attribution (the budgets COALESCE pattern): a bill carries the
-- bill-level bag, a bill item may override it; plain transactions carry
-- their own. SET NULL means deleting a bag leaves the money where it is,
-- just untagged. Refused on income/transfer rows by the service — income
-- cannot spend a reserve, transfers move money between accounts.
ALTER TABLE transactions ADD COLUMN bag_id INTEGER REFERENCES bags (id) ON DELETE SET NULL;
ALTER TABLE bills        ADD COLUMN bag_id INTEGER REFERENCES bags (id) ON DELETE SET NULL;
ALTER TABLE bill_items   ADD COLUMN bag_id INTEGER REFERENCES bags (id) ON DELETE SET NULL;