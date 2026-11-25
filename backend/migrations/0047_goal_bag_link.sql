-- Goals may now track a BAG instead of an account: the goal's progress is the
-- bag's live balance (Σ allocations − tagged spend), so "achieved" means money
-- actually reserved for the goal, not the whole account balance. bag_id is
-- nullable — a NULL keeps the legacy account-linked behavior (progress = the
-- account's balance), so existing goals keep working untouched. account_id
-- stays NOT NULL as the currency anchor: a bag-linked goal derives it from the
-- bag's account at save time (single source of truth). Deleting a bag cascades
-- its linked goal (ON DELETE CASCADE).
ALTER TABLE goals ADD COLUMN bag_id INTEGER REFERENCES bags (id) ON DELETE CASCADE;
