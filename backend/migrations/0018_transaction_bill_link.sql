-- Denormalize the bill→transaction edge onto the transaction row: the
-- transactions API needs to answer "was this row recorded for a scanned
-- bill?" with a single field, so the transaction stays readonly in the
-- activity view and the UI can link to the bill where it is edited.
-- bills.transaction_id already carries the reverse edge (0008).
ALTER TABLE transactions ADD COLUMN bill_id INTEGER REFERENCES bills (id) ON DELETE SET NULL;

CREATE INDEX idx_transactions_bill ON transactions (bill_id);

-- Backfill. Correlated subquery (portable; UPDATE...FROM needs SQLite ≥ 3.33).
-- One-to-one in practice — recordBillTransaction is the only writer — but
-- ORDER BY b.id keeps it deterministic if a stray duplicate ever existed.
UPDATE transactions
SET bill_id = (
    SELECT b.id FROM bills b
    WHERE b.transaction_id = transactions.id
    ORDER BY b.id LIMIT 1
)
WHERE EXISTS (SELECT 1 FROM bills b WHERE b.transaction_id = transactions.id);