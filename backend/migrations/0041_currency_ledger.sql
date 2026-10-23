-- Multi-currency ledger completeness (the fixes the currency audit asked for):
--
-- 1. fee_cents — a transfer's bank/intermediary fee, in the SOURCE account's
--    currency. The sending side loses amount + fee; the receiving side gains
--    only the converted amount. NULL = no fee (optional field).
--
-- 2. fx_rate — the exchange rate actually applied (units of `to` per 1 of
--    `from`), recorded whenever a conversion happened: the receiving leg of a
--    cross-currency transfer and a foreign-currency expense. The user can
--    override the public reference rate (banks don't always follow it); what
--    is stored is what was actually used.
--
-- 3. account_amount_cents — an income/expense row whose currency differs from
--    the owning account's currency, converted into the account's currency at
--    save time. This closes the 1:1 hole: a bill in COP paid from a EUR card
--    used to deduct 100000 from the EUR balance as if 1 COP = 1 EUR. NULL =
--    row currency == account currency (or no rate known — the same accepted
--    degradation as the counter leg).
--
-- No backfill: re-deriving old rows against today's rate would rewrite
-- history, and migration 0040 already folded past foreign-amount effects
-- into the opening balances. New rows convert correctly; old rows keep the
-- balances they produced.
ALTER TABLE transactions ADD COLUMN fee_cents INTEGER;
ALTER TABLE transactions ADD COLUMN fx_rate REAL;
ALTER TABLE transactions ADD COLUMN account_amount_cents INTEGER;