import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { transactionsApi, type TransactionFilters } from '../../api/transactions';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import type { Transaction, TransactionKind } from '../../types/domain';
import { formatCents, formatSignedCents, currentMonth } from '../../lib/money';
import { categoriesBySection } from '../../lib/categories';
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

  // Group consecutive transactions sharing a date (the list arrives sorted by
  // date desc) into "TODAY −$60.15"-style sections, like the reference design.
  const dayGroups: { date: string; label: string; total: number; currency: string; items: Transaction[] }[] = [];
  for (const t of transactions) {
    const last = dayGroups[dayGroups.length - 1];
    const signed = t.kind === 'income' ? t.amount_cents : -t.amount_cents;
    if (last && last.date === t.date) {
      last.total += signed;
      last.items.push(t);
    } else {
      dayGroups.push({
        date: t.date,
        label: dayLabel(t.date),
        total: signed,
        currency: t.currency,
        items: [t],
      });
    }
  }

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
            {categoriesBySection(categories.data ?? [], 'expense').map(([section, cats]) => (
              <optgroup key={section} label={section}>
                {cats.map((c) => (
                  <option key={c.id} value={c.id} title={c.description}>
                    {c.icon ? `${c.icon} ${c.name}` : c.name}
                  </option>
                ))}
              </optgroup>
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
      </div>
      <div className="segmented" role="group" aria-label="Filter by kind">
        {(
          [
            ['', 'All'],
            ['expense', 'Expenses'],
            ['income', 'Income'],
          ] as [TransactionKind | '', string][]
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            className={kind === value ? 'active' : ''}
            aria-pressed={kind === value}
            onClick={() => setKind(value)}
          >
            {label}
          </button>
        ))}
      </div>

      {transactions.length === 0 ? (
        <EmptyState message={`No transactions for ${month}.`} />
      ) : (
        <div className="day-group">
          {dayGroups.map((group) => (
            <section key={group.date} aria-label={`${group.label}, total ${formatCents(group.total, group.currency)}`}>
              <div className="day-header">
                <span>{group.label}</span>
                <span>{formatSignedCents(group.total, group.currency)}</span>
              </div>
              <ItemPanels>
                {group.items.map((t) => {
                  const isIncome = t.kind === 'income';
                  const cat = t.category_id ? categoryNames.get(t.category_id) : undefined;
                  const account = accountNames.get(t.account_id);
                  return (
                    <ItemPanel
                      key={t.id}
                      icon={isIncome ? '💰' : '🛒'}
                      title={t.description || '(no description)'}
                      subtitle={[cat ?? 'Uncategorized', account].filter(Boolean).join(' · ')}
                      value={formatCents(t.amount_cents, t.currency)}
                      valueClass={isIncome ? 'stat-positive' : ''}
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
                          <strong>{formatCents(t.amount_cents, t.currency)}</strong>
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
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

/** Section header for a transaction day: "Today", "Yesterday", or
 *  "SAT, SEP 12" for older dates. */
function dayLabel(iso: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  const day = new Date(y ?? 2000, (m ?? 1) - 1, d ?? 1);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const daysAgo = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (daysAgo === 0) return 'Today';
  if (daysAgo === 1) return 'Yesterday';
  return day
    .toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric' })
    .toUpperCase();
}