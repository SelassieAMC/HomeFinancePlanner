-- Fixed vs. discretionary expense classification for the analytics charts.
--
-- is_fixed marks expense categories as recurring commitments (rent,
-- utilities, insurance, fees) versus flexible spending. Only kind='expense'
-- rows are classified: bill-item categories (kind='product') are always
-- discretionary, so they keep the default. NULL/0 means discretionary.
--
-- The seeded list mirrors 0007/0014: regular same-amount-every-month
-- commitments. Variable-but-essential spending (fuel, pharmacy, home
-- maintenance) stays discretionary — it is not a fixed monthly amount.
ALTER TABLE categories ADD COLUMN is_fixed INTEGER NOT NULL DEFAULT 0;

UPDATE categories SET is_fixed = 1 WHERE kind = 'expense' AND name IN (
    'Rent & Mortgage',
    'Electricity',
    'Gas',
    'Water',
    'Waste & Recycling',
    'Internet & Telecom',
    'Insurance',
    'Car Insurance',
    'Taxes & Fees',
    'Bank Fees',
    'Streaming & Subscriptions',
    'Home Expenses'
);

-- Analytics scans filter accepted bills by date on nearly every query.
CREATE INDEX IF NOT EXISTS idx_bills_status_date ON bills (status, date);