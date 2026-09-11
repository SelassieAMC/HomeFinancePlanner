-- Native currency per transaction. Bills already carry one (bills.currency);
-- manual transactions take their account's currency, bill confirmations
-- record the bill's currency. Existing rows are backfilled from the linked
-- account (account_id is NOT NULL with ON DELETE CASCADE, so every row has
-- a match) — blanket-defaulting them to 'USD' would mislabel a non-USD
-- account's history and convert it wrongly forever.
ALTER TABLE transactions ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD';

UPDATE transactions
SET currency = (SELECT a.currency FROM accounts a WHERE a.id = transactions.account_id);