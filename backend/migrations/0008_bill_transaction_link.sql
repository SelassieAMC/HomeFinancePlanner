-- Track the expense transaction created when a bill is confirmed, so later
-- bill edits can keep the transaction amount/date in sync.
ALTER TABLE bills ADD COLUMN transaction_id INTEGER REFERENCES transactions (id) ON DELETE SET NULL;