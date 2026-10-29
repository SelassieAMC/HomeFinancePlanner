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
import { formatCents, formatSignedCents } from '../../lib/money';
import {
  Button,
  Card,
  CategorySelect,
  Dialog,
  Field,
  Spinner,
  ErrorMessage,
  EmptyState,
  IconButton,
  IconCloseCircle,
  IconEdit,
  IconMoveBetween,
  IconReopen,
  IconTrash,
  ProgressBar,
} from '../../components/ui';
import type { TFunction } from 'i18next';

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
// current month's successor with the same amount. A carrying series
// (carry_over) rolls its running balance — leftover or deficit — into the
// successor's available money, so the list doubles as the Jan-vs-Feb history
// while the current row reflects the full envelope chain.
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
  // Monthly series may roll the running leftover into the next month
  // (deficits negative — MoneyWiz envelopes). On by default; envelopes never
  // carry.
  const [carry, setCarry] = useState(true);
  const [formError, setFormError] = useState<string | null>(null);

  // Amount editing: only open rows offer it (the backend refuses closed ones
  // — a closed row's amount is settled history).
  const [editing, setEditing] = useState<Budget | null>(null);
  const [editAmount, setEditAmount] = useState('');
  const [editCarry, setEditCarry] = useState(true);
  const [editError, setEditError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function openEdit(b: Budget) {
    setEditing(b);
    setEditAmount((b.amount_cents / 100).toFixed(2));
    setEditCarry(b.carry_over ?? true);
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
      await budgetsApi.updateAmount(
        editing.id,
        cents,
        editing.cadence === 'monthly' ? editCarry : undefined,
      );
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
        carry_over: cadence === 'monthly' ? carry : undefined,
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

  // Envelope transfers (moneywiz roadmap Phase 3): move allowance between
  // open monthly rows of this same month — or, on a carrying series, seed
  // the row's running carried balance "from nothing".
  // The audit history reloads whenever the table reloads (a transfer is
  // visible in both).
  const transferHistory = useAsync(() => budgetsApi.transfers(), [data]);

  const [transferring, setTransferring] = useState<Budget | null>(null);
  const [transferMode, setTransferMode] = useState<'move' | 'seed'>('move');
  const [transferTo, setTransferTo] = useState('');
  const [transferAmount, setTransferAmount] = useState('');
  const [transferNote, setTransferNote] = useState('');
  const [transferError, setTransferError] = useState<string | null>(null);

  function openTransfer(b: Budget) {
    setTransferring(b);
    setTransferMode('move');
    setTransferTo('');
    setTransferAmount('');
    setTransferNote('');
    setTransferError(null);
  }

  async function handleTransfer(e: React.FormEvent) {
    e.preventDefault();
    if (!transferring) return;
    const cents = dollarsToCents(transferAmount);
    if (!Number.isFinite(cents) || (transferMode === 'move' && cents <= 0)) {
      setTransferError(t('validation.positive_amount'));
      return;
    }
    const target = transferMode === 'seed' ? transferring.id : Number(transferTo);
    if (!target) {
      setTransferError(t('transfer.choose_target'));
      return;
    }
    try {
      await budgetsApi.transfer({
        // Absent from_budget_id = carried-balance seed.
        from_budget_id: transferMode === 'move' ? transferring.id : undefined,
        to_budget_id: target,
        amount_cents: cents,
        note: transferNote.trim() || undefined,
      });
      setTransferring(null);
      reload();
    } catch (err) {
      setTransferError(err instanceof Error ? err.message : t('transfer.error'));
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
          <Field label={t('add.category_label')}>
            <CategorySelect
              categories={categories.data ?? []}
              kind="expense"
              value={categoryId ? Number(categoryId) : null}
              onChange={(id) => setCategoryId(id ? String(id) : '')}
              ariaLabel={t('add.category_aria')}
              emptyLabel={t('add.category_empty')}
            />
          </Field>
          <Field
            label={cadence === 'monthly' ? t('add.amount_monthly_label', { currency }) : t('add.amount_open_label', { currency })}
          >
            <input
              placeholder={t('add.amount_placeholder', { currency })}
              inputMode="decimal"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              required
            />
          </Field>
          <Field
            label={t('add.cadence_label')}
            hint={t('add.cadence_hint')}
          >
            <select
              value={cadence}
              onChange={(e) => setCadence(e.target.value as BudgetCadence)}
              aria-label={t('add.cadence_aria')}
            >
              <option value="monthly">{t('add.cadence_monthly')}</option>
              <option value="open">{t('add.cadence_open')}</option>
            </select>
          </Field>
          <div className="form-footer">
            {cadence === 'monthly' && (
              <label className="default-radio" aria-label={t('add.carry_over_aria')}>
                <input
                  type="checkbox"
                  checked={carry}
                  onChange={(e) => setCarry(e.target.checked)}
                />
                {t('add.carry_over')}
              </label>
            )}
            <Button type="submit">{t('add.submit')}</Button>
          </div>
        </form>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {budgets.length === 0 ? (
        <EmptyState message={t('empty')} />
      ) : (
        <div className="table-scroll">
          <table className="plan-table">
            <thead>
              <tr>
                <th>{t('table.budget')}</th>
                <th>{t('table.period')}</th>
                <th className="num">{t('table.spent')}</th>
                <th className="num">{t('table.cap')}</th>
                <th>{t('table.progress')}</th>
                <th>{t('table.status')}</th>
                <th aria-hidden="true" />
              </tr>
            </thead>
            <tbody>
              {budgets.map((b) => {
                const lifetime = lifetimeSpentByBudget.get(b.id) ?? 0;
                // A carrying monthly row's available money is cap + running
                // carry (deficits negative) − month spend.
                const carried = b.carry_over ? b.carried_cents ?? 0 : 0;
                const available = b.amount_cents + carried - lifetime;
                return (
                  <tr key={b.id}>
                    <td>
                      <span className="cell-entity">
                        {(categories.data ?? []).find((c) => c.id === b.category_id)?.icon && (
                          <span aria-hidden="true">
                            {(categories.data ?? []).find((c) => c.id === b.category_id)?.icon}
                          </span>
                        )}
                        <span className="entity-name">
                          {categoryNames.get(b.category_id) ?? `#${b.category_id}`}
                        </span>
                      </span>
                    </td>
                    <td className="cell-muted">
                      {b.cadence === 'monthly'
                        ? budgetPeriodLabel(t, b.month ?? '')
                        : t('table.envelope')}
                    </td>
                    <td className="num">
                      <strong>{formatCents(lifetime, currency)}</strong>
                    </td>
                    <td className="num">
                      <span className={b.status === 'closed' ? 'cell-muted' : undefined}>
                        {formatCents(b.amount_cents, currency)}
                      </span>
                      {carried !== 0 && (
                        <span
                          className={carried < 0 ? '' : 'ok'}
                          style={{ display: 'block', fontSize: '0.85em' }}
                        >
                          {t('card.carried', { amount: formatCents(carried, currency) })}
                        </span>
                      )}
                    </td>
                    <td className="cell-progress">
                      <ProgressBar
                        pct={b.amount_cents > 0 ? (lifetime / b.amount_cents) * 100 : 0}
                        over={available < 0}
                      />
                    </td>
                    <td>
                      <BudgetStatusCell
                        isClosed={b.status === 'closed'}
                        over={available < 0}
                        remaining={available}
                        currency={currency}
                      />
                    </td>
                    <td className="cell-actions">
                      {b.status !== 'closed' && (
                        <>
                          {b.cadence === 'monthly' && (
                            <IconButton
                              icon={<IconMoveBetween />}
                              label={t('actions.transfer')}
                              disabled={busy}
                              onClick={() => openTransfer(b)}
                            />
                          )}
                          <IconButton
                            icon={<IconEdit />}
                            label={t('card.edit')}
                            disabled={busy}
                            onClick={() => openEdit(b)}
                          />
                          <IconButton
                            icon={<IconCloseCircle />}
                            label={t('card.mark_finished')}
                            disabled={busy}
                            onClick={() => handleStatus(b.id, 'closed')}
                          />
                        </>
                      )}
                      {b.status === 'closed' && (
                        <IconButton
                          icon={<IconReopen />}
                          label={t('card.reopen')}
                          disabled={busy}
                          onClick={() => handleStatus(b.id, 'open')}
                        />
                      )}
                      <IconButton
                        icon={<IconTrash />}
                        label={t('card.delete')}
                        variant="danger"
                        disabled={busy}
                        onClick={() => handleDelete(b.id)}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {editing && (
        <Dialog
          onClose={() => setEditing(null)}
          title={
            editing.cadence === 'monthly'
              ? t('edit.title_monthly', { month: monthTag(editing.month ?? '') })
              : t('edit.title')
          }
        >
          <form className="form-row" onSubmit={handleEdit}>
            <Field
              label={
                editing.cadence === 'monthly'
                  ? t('add.amount_monthly_label', { currency })
                  : t('add.amount_open_label', { currency })
              }
              hint={t('edit.hint')}
              className="edit-amount-field"
            >
              <input
                placeholder={t('add.amount_placeholder', { currency })}
                inputMode="decimal"
                value={editAmount}
                onChange={(e) => setEditAmount(e.target.value)}
                autoFocus
                aria-label={t('add.amount_aria')}
                required
              />
            </Field>
            {editing.cadence === 'monthly' && (
              <label className="default-radio" aria-label={t('edit.carry_over_aria')}>
                <input
                  type="checkbox"
                  checked={editCarry}
                  onChange={(e) => setEditCarry(e.target.checked)}
                />
                {t('add.carry_over')}
              </label>
            )}
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

      {transferring && (
        <Dialog
          onClose={() => setTransferring(null)}
          title={t('transfer.title', { month: monthTag(transferring.month ?? '') })}
        >
          <form className="form-stack" onSubmit={handleTransfer}>
            {transferring.carry_over && (
              <label className="default-radio" aria-label={t('transfer.seed_aria')}>
                <input
                  type="checkbox"
                  checked={transferMode === 'seed'}
                  onChange={(e) => setTransferMode(e.target.checked ? 'seed' : 'move')}
                />
                {t('transfer.seed')}
              </label>
            )}
            {transferMode === 'move' && (
              <Field label={t('transfer.target_label')}>
                <select
                  value={transferTo}
                  onChange={(e) => setTransferTo(e.target.value)}
                  aria-label={t('transfer.target_aria')}
                  required
                >
                  <option value="">{t('transfer.choose_target')}</option>
                  {budgets
                    .filter(
                      (o) =>
                        o.id !== transferring.id &&
                        o.cadence === 'monthly' &&
                        o.status === 'open' &&
                        o.month === transferring.month,
                    )
                    .map((o) => (
                      <option key={o.id} value={o.id}>
                        {categoryNames.get(o.category_id) ?? `#${o.category_id}`}
                      </option>
                    ))}
                </select>
              </Field>
            )}
            <Field
              label={transferMode === 'seed' ? t('transfer.seed_label', { currency }) : t('transfer.amount_label', { currency })}
              hint={transferMode === 'seed' ? t('transfer.seed_hint') : t('transfer.hint')}
            >
              <input
                placeholder={
                  transferMode === 'seed'
                    ? t('transfer.seed_amount', { currency })
                    : t('add.amount_placeholder', { currency })
                }
                inputMode="decimal"
                value={transferAmount}
                onChange={(e) => setTransferAmount(e.target.value)}
                required
                autoFocus
              />
            </Field>
            <Field label={t('transfer.note_label')}>
              <input
                placeholder={t('transfer.note')}
                maxLength={200}
                value={transferNote}
                onChange={(e) => setTransferNote(e.target.value)}
              />
            </Field>
            {transferError && <ErrorMessage message={transferError} />}
            <div className="dialog-actions">
              <Button variant="ghost" onClick={() => setTransferring(null)} disabled={busy}>
                {t('edit.cancel')}
              </Button>
              <Button variant="primary" type="submit" disabled={busy}>
                {transferMode === 'seed' ? t('transfer.seed_submit') : t('transfer.submit')}
              </Button>
            </div>
          </form>
        </Dialog>
      )}

      {(transferHistory.data ?? []).length > 0 && (
        <Card title={t('transfer.history_title')}>
          <div className="table-scroll">
            <table className="plan-table">
              <tbody>
                {(transferHistory.data ?? []).map((tr) => (
                  <tr key={tr.id}>
                    <td className="cell-muted">{tr.month}</td>
                    <td>
                      {tr.from_budget_id == null
                        ? t('transfer.from_nothing')
                        : tr.from_category_name || `#${tr.from_category_id ?? ''}`}
                    </td>
                    <td aria-hidden="true">→</td>
                    <td>{tr.to_category_name || `#${tr.to_category_id}`}</td>
                    <td className="num">
                      <strong>{formatSignedCents(tr.amount_cents, currency)}</strong>
                    </td>
                    <td className="cell-muted">{tr.note || ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
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

// BudgetCard's progress-card became a table row: the cadence/month cell,
// aligned figure cells, a bar cell and the icon actions.
function budgetPeriodLabel(t: TFunction, month: string): string {
  return month ? `${monthTag(month)}` : t('card.monthly');
}

/** Status wording of a budget row, colored by state. */
function BudgetStatusCell({
  isClosed,
  over,
  remaining,
  currency,
}: {
  isClosed: boolean;
  over: boolean;
  remaining: number;
  currency: string;
}) {
  const { t } = useTranslation('budgets');
  const label = over
    ? t('card.over_by', { amount: formatCents(-remaining, currency) })
    : t('card.left', { amount: formatCents(remaining, currency) });
  if (isClosed) {
    return (
      <span className="cell-status">
        <span className="budget-dot closed" aria-hidden="true" />
        {t('table.closed')}
      </span>
    );
  }
  return (
    <span className={`cell-status${over ? ' over' : ''}`}>
      <span className={over ? 'budget-dot over' : 'budget-dot'} aria-hidden="true" />
      {over ? label : <span className="ok">{label}</span>}
    </span>
  );
}