-- Budgets become open-ended envelopes: a spending pool for one category that
-- stays open until it is manually marked finished and closed, so lifetime
-- spend, savings and overspend can be evaluated per budget (e.g. vacations).
-- Month scoping is dropped entirely — budgets apply to any bill date.
--
-- Start fresh: existing per-month budget rows are dropped, and bill/bill-item
-- links to them are cleared (the old ids have no meaning under the new model).
UPDATE bills      SET budget_id = NULL;
UPDATE bill_items SET budget_id = NULL;
DROP TABLE budgets;
CREATE TABLE budgets (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id  INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    amount_cents INTEGER NOT NULL CHECK (amount_cents > 0),
    status       TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    closed_at    INTEGER,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
-- One open envelope per category; closed ones remain as history and a new
-- budget for the same category may be started later.
CREATE UNIQUE INDEX budgets_one_open_per_category
    ON budgets (category_id) WHERE status = 'open';