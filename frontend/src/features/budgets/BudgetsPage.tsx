import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { useAsync } from '../../hooks/useAsync';
import { budgetsApi } from '../../api/budgets';
import { dollarsToCents } from '../../lib/money';
import { summaryApi } from '../../api/summary';
import { categoriesApi } from '../../api/categories';
import type { Budget, BudgetCadence } from '../../types/domain';
import { formatCents } from '../../lib/money';
import {
  Button,
  Card,
  CategorySelect,
  Dialog,
  Spinner,
  ErrorMessage,
  EmptyState,
} from '../../components/ui';

/** Short month label for a monthly budget's period ("2026-10" → "Oct 2026",
 *  localized to the active UI language). */
function monthTag(month: string): string {
  return new Date(`${month}-01T00:00:00`).toLocaleDateString(intlLocale(i18n.language), {
    month: 'short',
    year: 'numeric',
  });
}

// Budgets come in two cadences. Envelopes stay open — accumulating attributed
// spend — until marked finished and closed; progress is lifetime, so a closed
// vacations budget shows how much was spent, saved or overspent. Monthly
// budgets are fixed caps for the calendar month they carry: a lazy rollover
// closes the stale monthly row once its month has passed and opens the
// current month's successor with the same amount (leftovers never roll
// over), so the list doubles as the Jan-vs-Feb history.
export function BudgetsPage({ embedded = false }: { embedded?: boolean }) {
  const { t } = useTranslation('budgets');
  const { data, loading, error, reload } = useAsync(() => budgetsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  // Lifetime spend per budget (month-bounded for monthly rows). The
  // summary's lifetime fields are range-independent, and a range covering
  // all time makes every budget with any attributed spend show up (closed
  // ones included).
  const summary = useAsync(
    () => summaryApi.range('1970-01-01', '2999-12-31'),
    [],
  );

  const [categoryId, setCategoryId] = useState('');
  const [amount, setAmount] = useState('');
  // Monthly is the common case (groceries, dining out); envelopes cover
  // irregular pools like clothes or vacations.
  const [cadence, setCadence] = useState<BudgetCadence>('monthly');
  const [formError, setFormError] = useState<string | null>(null);

  // Amount editing: only open rows offer it (the backend refuses closed ones
  // — a closed row's amount is settled history).
  const [editing, setEditing] = useState<Budget | null>(null);
  const [editAmount, setEditAmount] = useState('');
  const [editError, setEditError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function openEdit(b: Budget) {
    setEditing(b);
    setEditAmount((b.amount_cents / 100).toFixed(2));
    setEditError(null);
  }

  async function handleEdit(e: React.FormEvent) {
    e.preventDefault();
    if (!editing) return;
    const cents = dollarsToCents(editAmount);
    if (!Number.isFinite(cents) || cents <= 0) {
      setEditError(t('validation.positive_amount'));
      return;
    }
    setBusy(true);
    setEditError(null);
    try {
      await budgetsApi.updateAmount(editing.id, cents);
      setEditing(null);
      reload();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : t('errors.update'));
    } finally {
      setBusy(false);
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);

    const cents = dollarsToCents(amount); // parses the comma decimal separator too
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError(t('validation.positive_amount'));
      return;
    }
    if (!categoryId) {
      setFormError(t('validation.choose_category'));
      return;
    }
    try {
      await budgetsApi.create({
        category_id: Number(categoryId),
        amount_cents: cents,
        cadence,
      });
      setAmount('');
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
    }
  }

  async function handleStatus(id: number, status: 'open' | 'closed') {
    try {
      await budgetsApi.setStatus(id, status);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.update'));
    }
  }

  async function handleDelete(id: number) {
    try {
      await budgetsApi.remove(id);
      reload();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.delete'));
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

  // Open budgets first — the ones still accepting spend; closed rows newest
  // month first so monthly history reads chronologically.
  const budgets = [...(data ?? [])].sort((a, b) => {
    const open = (a.status === 'open' ? 0 : 1) - (b.status === 'open' ? 0 : 1);
    if (open !== 0) return open;
    return (b.month ?? '').localeCompare(a.month ?? '');
  });

  const content = (
    <>
      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <CategorySelect
            categories={categories.data ?? []}
            kind="expense"
            value={categoryId ? Number(categoryId) : null}
            onChange={(id) => setCategoryId(id ? String(id) : '')}
            ariaLabel={t('add.category_aria')}
            emptyLabel={t('add.category_empty')}
          />
          <input
            placeholder={t('add.amount_placeholder', { currency })}
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
          <select
            value={cadence}
            onChange={(e) => setCadence(e.target.value as BudgetCadence)}
            aria-label={t('add.cadence_aria')}
          >
            <option value="monthly">{t('add.cadence_monthly')}</option>
            <option value="open">{t('add.cadence_open')}</option>
          </select>
          <Button type="submit">{t('add.submit')}</Button>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {budgets.length === 0 ? (
        <EmptyState message={t('empty')} />
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
                onEdit={() => openEdit(b)}
                onDelete={() => handleDelete(b.id)}
              />
            );
          })}
        </div>
      )}

      {editing && (
        <Dialog
          title={
            editing.cadence === 'monthly'
              ? t('edit.title_monthly', { month: monthTag(editing.month ?? '') })
              : t('edit.title')
          }
        >
          <form className="form-row" onSubmit={handleEdit}>
            <input
              placeholder={t('add.amount_placeholder', { currency })}
              inputMode="decimal"
              value={editAmount}
              onChange={(e) => setEditAmount(e.target.value)}
              autoFocus
              aria-label={t('add.amount_aria')}
              required
            />
            <p className="hint-text">{t('edit.hint')}</p>
            {editError && <ErrorMessage message={editError} />}
            <div className="dialog-actions">
              <Button variant="ghost" onClick={() => setEditing(null)} disabled={busy}>
                {t('edit.cancel')}
              </Button>
              <Button variant="primary" type="submit" disabled={busy}>
                {t('edit.save')}
              </Button>
            </div>
          </form>
        </Dialog>
      )}
    </>
  );

  if (embedded) return content;

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>
      {content}
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
  onEdit,
  onDelete,
}: {
  budget: Budget;
  categoryName: string;
  categoryIcon?: string;
  lifetimeSpent: number;
  currency: string;
  onSetStatus: (status: 'open' | 'closed') => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const { t } = useTranslation('budgets');
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
        {budget.cadence === 'monthly' && (
          <span className="stat-label">
            {t('card.monthly')}{budget.month ? ` · ${monthTag(budget.month)}` : ''}
          </span>
        )}
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
          ? t('card.closed', {
              spent: formatCents(lifetimeSpent, currency),
              amount: formatCents(budget.amount_cents, currency),
            })
          : over
            ? t('card.over_by', { amount: formatCents(-remaining, currency) })
            : t('card.left', { amount: formatCents(remaining, currency) })}
      </div>
      <div className="budget-actions">
        {isClosed ? (
          <Button onClick={() => onSetStatus('open')}>{t('card.reopen')}</Button>
        ) : (
          <>
            <Button variant="secondary" className="btn-sm" onClick={onEdit}>
              {t('card.edit')}
            </Button>
            <Button variant="secondary" className="btn-sm" onClick={() => onSetStatus('closed')}>
              {t('card.mark_finished')}
            </Button>
          </>
        )}
        <Button variant="danger" className="btn-sm" onClick={onDelete}>
          {t('card.delete')}
        </Button>
      </div>
    </Card>
  );
}