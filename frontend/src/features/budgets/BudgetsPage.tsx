import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { budgetsApi } from '../../api/budgets';
import { summaryApi } from '../../api/summary';
import { categoriesApi } from '../../api/categories';
import type { Budget } from '../../types/domain';
import { formatCents } from '../../lib/money';
import { categoriesBySection } from '../../lib/categories';
import { Button, Card, Spinner, ErrorMessage, EmptyState } from '../../components/ui';

// Budgets are open-ended envelopes: they stay open — accumulating attributed
// spend — until they are marked finished and closed. Progress is lifetime,
// not monthly: the point is to see how much was spent, saved or overspent on
// e.g. a vacations budget once it is closed.
export function BudgetsPage() {
  const { data, loading, error, reload } = useAsync(() => budgetsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  // Lifetime spend per budget. The summary's lifetime fields are
  // range-independent, and a range covering all time makes every envelope
  // with any attributed spend show up (closed ones included).
  const summary = useAsync(
    () => summaryApi.range('1970-01-01', '2999-12-31'),
    [],
  );

  const [categoryId, setCategoryId] = useState('');
  const [amount, setAmount] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = Math.round(Number(amount) * 100);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError('Enter a positive budget amount.');
      return;
    }
    if (!categoryId) {
      setFormError('Choose a category.');
      return;
    }
    try {
      await budgetsApi.create({
        category_id: Number(categoryId),
        amount_cents: cents,
      });
      setAmount('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create budget.');
    }
  }

  async function handleStatus(id: number, status: 'open' | 'closed') {
    try {
      await budgetsApi.setStatus(id, status);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to update budget.');
    }
  }

  async function handleDelete(id: number) {
    try {
      await budgetsApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to delete budget.');
    }
  }

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const lifetimeSpentByBudget = new Map(
    (summary.data?.budgets ?? []).map((b) => [b.id, b.lifetime_spent_cents]),
  );
  const categoryNames = new Map((categories.data ?? []).map((c) => [c.id, c.name]));
  // Budget amounts and spend are denominated in the base currency.
  const currency = summary.data?.currency ?? 'USD';

  // Open envelopes first — the ones still accepting spend.
  const budgets = [...(data ?? [])].sort(
    (a, b) => (a.status === 'open' ? 0 : 1) - (b.status === 'open' ? 0 : 1),
  );

  return (
    <div className="page">
      <h2 className="page-title">Budgets</h2>

      <Card title="Add budget">
        <form className="form-row" onSubmit={handleCreate}>
          <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} required>
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
            placeholder={`Budget amount (${currency})`}
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
          <Button type="submit">Add</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {budgets.length === 0 ? (
        <EmptyState message="No budgets yet — add your first one above." />
      ) : (
        <div className="budget-list">
          {budgets.map((b) => {
            const lifetime = lifetimeSpentByBudget.get(b.id) ?? 0;
            return (
              <BudgetCard
                key={b.id}
                budget={b}
                categoryName={categoryNames.get(b.category_id) ?? `#${b.category_id}`}
                categoryIcon={
                  (categories.data ?? []).find((c) => c.id === b.category_id)?.icon
                }
                lifetimeSpent={lifetime}
                currency={currency}
                onSetStatus={(status) => handleStatus(b.id, status)}
                onDelete={() => handleDelete(b.id)}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

// BudgetCard renders one envelope in the always-visible progress-card style of
// the reference design: dot + category + spent/amount up top, the bar, a
// "left"/"over by" line, and the status/delete actions underneath.
function BudgetCard({
  budget,
  categoryName,
  categoryIcon,
  lifetimeSpent,
  currency,
  onSetStatus,
  onDelete,
}: {
  budget: Budget;
  categoryName: string;
  categoryIcon?: string;
  lifetimeSpent: number;
  currency: string;
  onSetStatus: (status: 'open' | 'closed') => void;
  onDelete: () => void;
}) {
  const isClosed = budget.status === 'closed';
  const over = lifetimeSpent > budget.amount_cents;
  const remaining = budget.amount_cents - lifetimeSpent;
  const pct = budget.amount_cents > 0 ? (lifetimeSpent / budget.amount_cents) * 100 : 0;
  return (
    <Card className="budget-card">
      <div className="budget-head">
        <span
          className={
            isClosed ? 'budget-dot closed' : over ? 'budget-dot over' : 'budget-dot'
          }
          aria-hidden="true"
        />
        <span className="budget-name">
          {categoryIcon && <span aria-hidden="true">{categoryIcon} </span>}
          {categoryName}
        </span>
        <span className="budget-amounts">
          <strong>{formatCents(lifetimeSpent, currency)}</strong>
          {' / '}
          {formatCents(budget.amount_cents, currency)}
        </span>
      </div>
      <div className="progress-track">
        <div
          className={over ? 'progress-fill over' : 'progress-fill'}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </div>
      <div
        className={
          isClosed ? 'budget-status' : over ? 'budget-status over' : 'budget-status ok'
        }
      >
        {isClosed
          ? `Closed · spent ${formatCents(lifetimeSpent, currency)} of ${formatCents(
              budget.amount_cents,
              currency,
            )}`
          : over
            ? `Over by ${formatCents(-remaining, currency)}`
            : `${formatCents(remaining, currency)} left`}
      </div>
      <div className="budget-actions">
        {isClosed ? (
          <Button onClick={() => onSetStatus('open')}>Reopen</Button>
        ) : (
          <Button variant="secondary" onClick={() => onSetStatus('closed')}>
            Mark finished
          </Button>
        )}
        <Button variant="danger" onClick={onDelete}>
          Delete
        </Button>
      </div>
    </Card>
  );
}