import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { accountsApi } from '../../api/accounts';
import type { Account, AccountInput, AccountType } from '../../types/domain';
import { formatCents, dollarsToCents } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

const ACCOUNT_TYPES: AccountType[] = ['checking', 'savings', 'credit', 'cash', 'other'];

const accountIcons: Record<AccountType, string> = {
  checking: '🏦',
  savings: '🐷',
  credit: '💳',
  cash: '💵',
  other: '📦',
};

export function AccountsPage() {
  const { data, loading, error, reload } = useAsync(() => accountsApi.list(), []);
  const [name, setName] = useState('');
  const [type, setType] = useState<AccountType>('checking');
  const [balance, setBalance] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(balance);
    if (Number.isNaN(cents) || cents < 0) {
      setFormError('Enter a valid non-negative starting balance.');
      return;
    }
    const input: AccountInput = { name, type, currency: 'USD', balance_cents: cents };
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
          <input
            placeholder="Starting balance ($)"
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
                <Button variant="danger" onClick={() => handleDelete(a.id)}>
                  Delete account
                </Button>
              </div>
            </ItemPanel>
          ))}
        </ItemPanels>
      )}
    </div>
  );
}