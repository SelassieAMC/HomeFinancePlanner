-- Correlate accepted bills with budgets. The bill-level budget is the
-- default for every line; bill_items.budget_id stores a per-line override
-- (NULL = inherit the bill's budget). Spending attributed to a budget uses
-- COALESCE(bill_items.budget_id, bills.budget_id).
ALTER TABLE bills     ADD COLUMN budget_id INTEGER REFERENCES budgets (id) ON DELETE SET NULL;
ALTER TABLE bill_items ADD COLUMN budget_id INTEGER REFERENCES budgets (id) ON DELETE SET NULL;