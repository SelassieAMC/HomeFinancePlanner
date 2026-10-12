-- Budget cadence: alongside the open-ended envelopes (0015) there are now
-- monthly budgets — a fixed cap per category that resets every calendar
-- month. Monthly rows materialize one row per month: the lazy rollover in
-- BudgetService closes a stale open monthly row (keeping its month as the
-- historical label, no leftover carry-over) and creates the successor for
-- the current month with the same amount, so changing the cap never
-- rewrites past months. Envelope rows keep month NULL.
ALTER TABLE budgets ADD COLUMN cadence TEXT NOT NULL DEFAULT 'open'
    CHECK (cadence IN ('open','monthly'));
ALTER TABLE budgets ADD COLUMN month TEXT
    CHECK (month IS NULL OR month GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]');

-- The existing budgets_one_open_per_category partial unique index still
-- applies across cadences: a category has either one open envelope or one
-- open monthly row, never both.
CREATE INDEX idx_budgets_cadence_month ON budgets (cadence, month);
