-- 0044: budget→budget transfers (MoneyWiz rule 5) + the carried-balance seed.
--
-- A transfer MOVES ALLOWANCE between two OPEN monthly rows of the same
-- calendar month: the source cap shrinks, the target cap grows, and this row
-- is the auditable reallocation (unlike a pair of naked cap edits, the move
-- keeps its record). It adjusts budgeted amounts — it is NOT a bag
-- allocation and holds no money of its own.
--
-- from_budget_id NULL is the "carried balance" seed: a first-month manual
-- carry, applied as carried_cents on the target row itself (signed — a
-- negative seed models "already overspent"). Seeds are refused on rows whose
-- series does not carry (carry_over = 0): the carried number would be
-- unread. Deleting a budget cascades its transfer history (the audit rows
-- are meaningless without their rows; the caps they moved stay moved — cap
-- edits are the budgets' own history).
CREATE TABLE budget_transfers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    from_budget_id INTEGER REFERENCES budgets (id) ON DELETE CASCADE,
    to_budget_id   INTEGER NOT NULL REFERENCES budgets (id) ON DELETE CASCADE,
    month          TEXT    NOT NULL, -- the shared YYYY-MM of both rows
    amount_cents   INTEGER NOT NULL, -- transfer: positive magnitude; seed: signed
    note           TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);

CREATE INDEX idx_budget_transfers_to ON budget_transfers (to_budget_id);