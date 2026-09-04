import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { transactionsApi, type TransactionFilters } from '../../api/transactions';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import type { Transaction, TransactionKind } from '../../types/domain';
import { formatCents, currentMonth } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

export function TransactionsPage() {
  const [month, setMonth] = useState(currentMonth());
  const [kind, setKind] = useState<TransactionKind | ''>('');

  const filters: TransactionFilters = { month, limit: 100 };
  if (kind) filters.kind = kind;

  const { data, loading, error, reload } = useAsync(
    () => transactionsApi.list(filters),
    [month, kind],
  );
  const accounts = useAsync(() => accountsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);

  const [accountId, setAccountId] = useState('');
  const [categoryId, setCategoryId] = useState('');
  const [txKind, setTxKind] = useState<TransactionKind>('expense');
  const [amount, setAmount] = useState('');
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [description, setDescription] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = Math.round(Number(amount) * 100);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive amount.');
      return;
    }
    if (!accountId) {
      setFormError('Choose an account.');
      return;
    }
    try {
      await transactionsApi.create({
        account_id: Number(accountId),
        category_id: categoryId ? Number(categoryId) : null,
        kind: txKind,
        amount_cents: cents,
        description,
        date,
      });
      setAmount('');
      setDescription('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create transaction.');
    }
  }

  async function handleDelete(id: number) {
    try {
      await transactionsApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to delete transaction.');
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const transactions: Transaction[] = data ?? [];
  const categoryNames = new Map((categories.data ?? []).map((c) => [c.id, c.name]));
  const accountNames = new Map((accounts.data ?? []).map((a) => [a.id, a.name]));

  return (
    <div className="page">
      <h2 className="page-title">Transactions</h2>

      <Card title="Add transaction">
        <form className="form-row" onSubmit={handleCreate}>
          <select value={txKind} onChange={(e) => setTxKind(e.target.value as TransactionKind)}>
            <option value="expense">Expense</option>
            <option value="income">Income</option>
          </select>
          <input
            type="date"
            value={date}
            onChange={(e) => setDate(e.target.value)}
            required
          />
          <input
            placeholder="Amount ($)"
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
          <select value={accountId} onChange={(e) => setAccountId(e.target.value)} required>
            <option value="">Account…</option>
            {(accounts.data ?? []).map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
          <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)}>
            <option value="">Category…</option>
            {(categories.data ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          <input
            placeholder="Description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
          <Button type="submit">Add</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      <div className="filter-row">
        <input
          type="month"
          value={month}
          onChange={(e) => setMonth(e.target.value)}
          aria-label="Filter by month"
        />
        <select value={kind} onChange={(e) => setKind(e.target.value as TransactionKind | '')}>
          <option value="">All kinds</option>
          <option value="expense">Expenses only</option>
          <option value="income">Income only</option>
        </select>
      </div>

      {transactions.length === 0 ? (
        <EmptyState message={`No transactions for ${month}.`} />
      ) : (
        <ItemPanels>
          {transactions.map((t) => {
            const isIncome = t.kind === 'income';
            const cat = t.category_id ? categoryNames.get(t.category_id) : undefined;
            const account = accountNames.get(t.account_id);
            return (
              <ItemPanel
                key={t.id}
                icon={isIncome ? '💰' : '🛒'}
                title={t.description || '(no description)'}
                subtitle={`${t.date}${cat ? ` • ${cat}` : ''}`}
                value={`${isIncome ? '+' : '−'}${formatCents(t.amount_cents)}`}
                valueClass={isIncome ? 'stat-positive' : 'stat-negative'}
              >
                <div className="item-field-grid">
                  <div className="item-field">
                    <span>Date</span>
                    <span>{t.date}</span>
                  </div>
                  <div className="item-field">
                    <span>Kind</span>
                    <span>{t.kind}</span>
                  </div>
                  <div className="item-field">
                    <span>Amount</span>
                    <strong>{formatCents(t.amount_cents)}</strong>
                  </div>
                </div>
                <div className="item-field-grid">
                  <div className="item-field">
                    <span>Category</span>
                    <span>{cat ?? '—'}</span>
                  </div>
                  <div className="item-field">
                    <span>Account</span>
                    <span>{account ?? `#${t.account_id}`}</span>
                  </div>
                </div>
                <div className="camera-row">
                  <Button variant="danger" onClick={() => handleDelete(t.id)}>
                    Delete transaction
                  </Button>
                </div>
              </ItemPanel>
            );
          })}
        </ItemPanels>
      )}
    </div>
  );
}