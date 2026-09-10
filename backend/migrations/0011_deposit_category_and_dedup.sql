-- Deposit/refund handling and duplicate-receipt protection.
--
-- 1) allows_negative marks categories whose lines are money BACK (bottle
--    deposits — Pfand — and Leergut refunds). Lines in such a category may
--    carry negative prices/discounts and reduce the bill total, like the
--    name-based "Leergut" detection. Users cannot create negative-priced
--    items under ordinary product categories.
ALTER TABLE categories ADD COLUMN allows_negative INTEGER NOT NULL DEFAULT 0;

INSERT INTO categories (name, section, icon, description, is_system, kind, allows_negative, created_at) VALUES
    ('Deposit & Returns', 'Deposit', '♻️', 'Bottle/crate deposit (Pfand) and Leergut refunds — money back, amounts may be negative', 1, 'product', 1, strftime ('%s', 'now'));

-- 2) file_hash (sha256 hex of the uploaded bytes) detects re-uploads of the
--    same receipt: an active scan or a saved bill with the same hash makes a
--    new upload a conflict. Legacy rows keep '' (unindexed semantics are the
--    service's problem; the service always sets the hash for new uploads).
ALTER TABLE bill_scans ADD COLUMN file_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE bills ADD COLUMN file_hash TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_bill_scans_hash ON bill_scans (file_hash);
CREATE INDEX idx_bills_hash      ON bills (file_hash);