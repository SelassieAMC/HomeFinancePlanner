import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { budgetsApi } from '../../api/budgets';
import { summaryApi } from '../../api/summary';
import { categoriesApi } from '../../api/categories';
import type { Budget } from '../../types/domain';
import { formatCents, currentMonth } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage, EmptyState, ItemPanel, ItemPanels } from '../../components/ui';

export function BudgetsPage() {
  const [month, setMonth] = useState(currentMonth());
  const { data, loading, error, reload } = useAsync(
    () => budgetsApi.listByMonth(month),
    [month],
  );
  const summary = useAsync(() => summaryApi.month(month), [month]);
  const categories = useAsync(() => categoriesApi.list(), []);

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
        month,
        amount_cents: cents,
      });
      setAmount('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create budget.');
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

  const spentByCategory = new Map(
    (summary.data?.budgets ?? []).map((b) => [b.id, b.spent_cents]),
  );
  const categoryNames = new Map((categories.data ?? []).map((c) => [c.id, c.name]));

  return (
    <div className="page">
      <h2 className="page-title">Budgets — {month}</h2>

      <Card title="Add budget">
        <form className="form-row" onSubmit={handleCreate}>
          <input
            type="month"
            value={month}
            onChange={(e) => setMonth(e.target.value)}
            aria-label="Budget month"
          />
          <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} required>
            <option value="">Category…</option>
            {(categories.data ?? [])
              .filter((c) => (c.kind ?? 'expense') === 'expense')
              .map((c) => (
                <option key={c.id} value={c.id}>
                  {c.icon ? `${c.icon} ${c.name}` : c.name}
                </option>
              ))}
          </select>
          <input
            placeholder="Monthly limit ($)"
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
          <Button type="submit">Add</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {data?.length === 0 ? (
        <EmptyState message={`No budgets set for ${month}.`} />
      ) : (
        <ItemPanels>
          {(data ?? []).map((b) => {
            const spent = spentByCategory.get(b.id) ?? 0;
            return (
              <BudgetPanel
                key={b.id}
                budget={b}
                categoryName={categoryNames.get(b.category_id) ?? `#${b.category_id}`}
                categoryIcon={
                  (categories.data ?? []).find((c) => c.id === b.category_id)?.icon
                }
                spent={spent}
                onDelete={() => handleDelete(b.id)}
              />
            );
          })}
        </ItemPanels>
      )}
    </div>
  );
}
// BudgetPanel renders one budget as a collapsible panel: summary shows the
// category and the spent/limit; expanding reveals progress and actions.
function BudgetPanel({
  budget,
  categoryName,
  categoryIcon,
  spent,
  onDelete,
}: {
  budget: Budget;
  categoryName: string;
  categoryIcon?: string;
  spent: number;
  onDelete: () => void;
}) {
  const remaining = budget.amount_cents - spent;
  const pct = budget.amount_cents > 0 ? (spent / budget.amount_cents) * 100 : 0;
  return (
    <ItemPanel
      icon={categoryIcon ?? '🎯'}
      title={categoryName}
      subtitle={`${formatCents(spent)} of ${formatCents(budget.amount_cents)}`}
      value={formatCents(remaining)}
      valueClass={remaining < 0 ? 'stat-negative' : ''}
    >
      <div className="progress-track">
        <div
          className={remaining < 0 ? 'progress-fill over' : 'progress-fill'}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </div>
      <div className="item-field-grid">
        <div className="item-field">
          <span>Limit</span>
          <strong>{formatCents(budget.amount_cents)}</strong>
        </div>
        <div className="item-field">
          <span>Spent</span>
          <span>{formatCents(spent)}</span>
        </div>
        <div className="item-field">
          <span>Remaining</span>
          <span className={remaining < 0 ? 'stat-negative' : ''}>
            {formatCents(remaining)}
          </span>
        </div>
      </div>
      <div className="camera-row">
        <Button variant="danger" onClick={onDelete}>
          Delete budget
        </Button>
      </div>
    </ItemPanel>
  );
}
