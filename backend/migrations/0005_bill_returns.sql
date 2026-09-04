-- Bottle/crate deposit returns ("Leergut"): these lines are money back, so
-- their unit price and line total may be negative. is_return marks them.
ALTER TABLE bill_items ADD COLUMN is_return INTEGER NOT NULL DEFAULT 0;