-- Derived account balances: payments, incomes and transfers now move the
-- account balance (computed per read), so paying with an account finally
-- shows up. The user-maintained balance_cents becomes a derived number:
--
--   balance = opening_balance_cents + Σ(income) − Σ(expense) − Σ(transfers out)
--             + Σ(transfers in, counter leg)
--
-- The opening balance is the pre-ledger seed money: everything before the
-- migration's transaction history is folded into it ONCE by this backfill, so
-- the derived balance equals the previously stored balance_cents at migration
-- time — no visible jump, and every later transaction moves it.
ALTER TABLE accounts ADD COLUMN opening_balance_cents INTEGER NOT NULL DEFAULT 0;

-- Cross-currency transfers: the receiving leg (what the destination account
-- gains), recorded at transfer time from the FX snapshot. NULL = same-currency
-- (or unknown rate): reads fold the amount 1:1 — the accepted degradation.
-- These must exist before the backfill reads them.
ALTER TABLE transactions ADD COLUMN counter_amount_cents INTEGER;
ALTER TABLE transactions ADD COLUMN counter_currency TEXT;

UPDATE accounts SET opening_balance_cents =
	balance_cents
	- COALESCE((SELECT SUM(CASE t.kind WHEN 'income' THEN t.amount_cents ELSE -t.amount_cents END)
		FROM transactions t WHERE t.account_id = accounts.id AND t.kind IN ('income', 'expense')), 0)
	+ COALESCE((SELECT SUM(t.amount_cents)
		FROM transactions t WHERE t.account_id = accounts.id AND t.kind = 'transfer'), 0)
	- COALESCE((SELECT SUM(COALESCE(t.counter_amount_cents, t.amount_cents))
		FROM transactions t WHERE t.counter_account_id = accounts.id AND t.kind = 'transfer'), 0);