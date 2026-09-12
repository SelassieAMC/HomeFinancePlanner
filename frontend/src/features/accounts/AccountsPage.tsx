import { useEffect, useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { accountsApi } from '../../api/accounts';
import { settingsApi } from '../../api/settings';
import type { Account, AccountInput, AccountType } from '../../types/domain';
import { formatCents, dollarsToCents } from '../../lib/money';
import { COMMON_CURRENCIES, type CurrencyOption } from '../../lib/currencies';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

const ACCOUNT_TYPES: AccountType[] = ['checking', 'savings', 'credit', 'cash', 'other'];

const accountIcons: Record<AccountType, string> = {
  checking: '🏦',
  savings: '🐷',
  credit: '💳',
  cash: '💵',
  other: '📦',
};

// The backend accepts any 3-letter ISO currency, which can be wider than the
// picklist — always include the account's current currency so it stays
// selectable instead of rendering as a blank option.
function currencyOptions(current: string): CurrencyOption[] {
  return COMMON_CURRENCIES.some((c) => c.code === current)
    ? COMMON_CURRENCIES
    : [{ code: current, label: current }, ...COMMON_CURRENCIES];
}

export function AccountsPage() {
  const { data, loading, error, reload } = useAsync(() => accountsApi.list(), []);
  // New accounts default to the user's base display currency.
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);
  const [name, setName] = useState('');
  const [type, setType] = useState<AccountType>('checking');
  const [balance, setBalance] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  // Edit-in-place state: only one account is edited at a time, so a single
  // set of fields keyed by editingId is enough.
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editName, setEditName] = useState('');
  const [editType, setEditType] = useState<AccountType>('checking');
  const [editBalance, setEditBalance] = useState('');
  const [editCardDigits, setEditCardDigits] = useState('');

  // New accounts default to the user's base display currency (falls back to
  // USD until the setting loads).
  const defaultCurrency = baseCurrency.data?.currency ?? 'USD';
  const [currency, setCurrency] = useState(defaultCurrency);
  const [currencyTouched, setCurrencyTouched] = useState(false);
  useEffect(() => {
    if (!currencyTouched) setCurrency(defaultCurrency);
  }, [defaultCurrency, currencyTouched]);

  const [editCurrency, setEditCurrency] = useState('USD');

  function startEdit(a: Account) {
    setEditingId(a.id);
    setEditName(a.name);
    setEditType(a.type);
    setEditCurrency(a.currency);
    setEditBalance((a.balance_cents / 100).toFixed(2));
    setEditCardDigits(a.card_last_digits ?? '');
    setFormError(null);
  }

  function cancelEdit() {
    setEditingId(null);
    setFormError(null);
  }

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault();
    if (editingId === null) return;
    setFormError(null);

    const cents = dollarsToCents(editBalance);
    if (Number.isNaN(cents) || cents < 0) {
      setFormError('Enter a valid non-negative balance.');
      return;
    }
    const input: AccountInput = {
      name: editName,
      type: editType,
      currency: editCurrency,
      balance_cents: cents,
      card_last_digits: editCardDigits || undefined,
    };
    try {
      await accountsApi.update(editingId, input);
      setEditingId(null);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to update account.');
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(balance);
    if (Number.isNaN(cents) || cents < 0) {
      setFormError('Enter a valid non-negative starting balance.');
      return;
    }
    const input: AccountInput = { name, type, currency, balance_cents: cents };
    try {
      await accountsApi.create(input);
      setName('');
      setBalance('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create account.');
    }
  }

  async function handleDelete(id: number) {
    try {
      await accountsApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to delete account.');
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const accounts: Account[] = data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">Accounts</h2>

      <Card title="Add account">
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder="Name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <select value={type} onChange={(e) => setType(e.target.value as AccountType)}>
            {ACCOUNT_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <select
            value={currency}
            onChange={(e) => {
              setCurrency(e.target.value);
              setCurrencyTouched(true);
            }}
            aria-label="Account currency"
          >
            {COMMON_CURRENCIES.map((c) => (
              <option key={c.code} value={c.code}>
                {c.code}
              </option>
            ))}
          </select>
          <input
            placeholder={`Starting balance (${currency})`}
            inputMode="decimal"
            value={balance}
            onChange={(e) => setBalance(e.target.value)}
            required
          />
          <Button type="submit">Add</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {accounts.length === 0 ? (
        <EmptyState message="No accounts yet — add your first one above." />
      ) : (
        <ItemPanels>
          {accounts.map((a) => (
            <ItemPanel
              key={a.id}
              icon={accountIcons[a.type] ?? '📦'}
              title={a.name}
              subtitle={a.type}
              value={formatCents(a.balance_cents, a.currency)}
            >
              {editingId === a.id ? (
                <form className="form-stack" onSubmit={handleUpdate}>
                  <input
                    placeholder="Name"
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    required
                  />
                  <select
                    value={editType}
                    onChange={(e) => setEditType(e.target.value as AccountType)}
                    aria-label="Account type"
                  >
                    {ACCOUNT_TYPES.map((t) => (
                      <option key={t} value={t}>
                        {t}
                      </option>
                    ))}
                  </select>
                  <select
                    value={editCurrency}
                    onChange={(e) => setEditCurrency(e.target.value)}
                    aria-label="Account currency"
                  >
                    {currencyOptions(editCurrency).map((c) => (
                      <option key={c.code} value={c.code}>
                        {c.code}
                      </option>
                    ))}
                  </select>
                  <input
                    placeholder={`Balance (${editCurrency})`}
                    inputMode="decimal"
                    value={editBalance}
                    onChange={(e) => setEditBalance(e.target.value)}
                    required
                  />
                  <input
                    placeholder="Card last digits (optional)"
                    inputMode="numeric"
                    maxLength={4}
                    value={editCardDigits}
                    onChange={(e) => setEditCardDigits(e.target.value.replace(/\D/g, ''))}
                  />
                  <div className="camera-row">
                    <Button type="submit">Save</Button>
                    <Button type="button" variant="secondary" onClick={cancelEdit}>
                      Cancel
                    </Button>
                  </div>
                </form>
              ) : (
                <>
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>Type</span>
                      <span>{a.type}</span>
                    </div>
                    <div className="item-field">
                      <span>Currency</span>
                      <span>{a.currency}</span>
                    </div>
                    <div className="item-field">
                      <span>Balance</span>
                      <strong>{formatCents(a.balance_cents, a.currency)}</strong>
                    </div>
                  </div>
                  {a.card_last_digits && (
                    <div className="item-field">
                      <span>Card</span>
                      <span>•••• {a.card_last_digits}</span>
                    </div>
                  )}
                  <div className="camera-row">
                    <Button onClick={() => startEdit(a)}>Edit</Button>
                    <Button variant="danger" onClick={() => handleDelete(a.id)}>
                      Delete account
                    </Button>
                  </div>
                </>
              )}
            </ItemPanel>
          ))}
        </ItemPanels>
      )}
    </div>
  );
}