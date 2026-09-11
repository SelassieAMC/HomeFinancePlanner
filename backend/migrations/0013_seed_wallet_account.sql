-- Seed the default wallet account: bills confirmed without picking an account
-- are recorded against it (wallet/cash money). Currency follows the stored
-- base display currency, defaulting to USD on a fresh database. The NOT
-- EXISTS guard keeps a pre-existing "Wallet" account (any case) from being
-- duplicated.
INSERT INTO accounts (name, type, currency, balance_cents, card_last_digits, created_at, updated_at)
SELECT 'Wallet', 'cash',
       COALESCE((SELECT value FROM settings WHERE key = 'base_currency'), 'USD'),
       0, '',
       CAST(strftime('%s', 'now') AS INTEGER),
       CAST(strftime('%s', 'now') AS INTEGER)
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE lower(name) = 'wallet');