import { useMemo, useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { schedulesApi } from '../../api/schedules';
import { accountsApi } from '../../api/accounts';
import { bagsApi } from '../../api/bags';
import { categoriesApi } from '../../api/categories';
import { settingsApi } from '../../api/settings';
import { dollarsToCents, formatCents } from '../../lib/money';
import { todayISO } from '../../lib/date';
import type {
  Account,
  BagStatus,
  Category,
  ScheduleKind,
  SchedulePayload,
  ScheduleRecurrence,
  ScheduleRunStatus,
  ScheduleRunView,
  ScheduledAction,
  ScheduledActionInput,
} from '../../types/domain';
import {
  Button,
  Card,
  Dialog,
  ErrorMessage,
  EmptyState,
  Spinner,
} from '../../components/ui';

// Scheduled actions are the unified scheduler: virtual kinds (bag funding /
// bag rebalancing) are applied by the system on their due date — the catch-up
// runs on every read, so there is nothing to click — while real movements
// (transfers, transactions) and purchases are confirm-first: they sit in the
// to-do feed until one click applies, dismisses or skips them. Edits affect
// future occurrences only; history rows are immutable.

const KIND_LABELS: Record<ScheduleKind, string> = {
  bag_funding: 'Fund bag',
  bag_rebalance: 'Rebalance bags',
  account_transfer: 'Account transfer',
  transaction: 'Transaction',
  purchase: 'Purchase',
};

// Virtual kinds never offer Apply: auto-applied on the due date (they only
// write bag ledger rows, never a stored account balance).
const VIRTUAL_KINDS: readonly ScheduleKind[] = ['bag_funding', 'bag_rebalance'];

const RECURRENCE_LABELS: Record<ScheduleRecurrence, string> = {
  once: 'Once',
  monthly: 'Monthly',
  yearly: 'Yearly',
};

const STATUS_LABELS: Record<ScheduleRunStatus, string> = {
  pending: 'To do',
  applied: 'Applied',
  dismissed: 'Dismissed',
  skipped: 'Skipped',
};

function dueLabel(due: string, today: string): string {
  if (due < today) {
    const days = daysBetween(due, today);
    return days === 1 ? '1 day overdue' : `${days} days overdue`;
  }
  if (due === today) return 'Due today';
  const days = daysBetween(today, due);
  return days === 1 ? 'Due tomorrow' : `Due in ${days} days`;
}

/** Whole days from `from` to `to` (string dates, no timezone traps). */
function daysBetween(from: string, to: string): number {
  const a = new Date(`${from}T00:00:00`).getTime();
  const b = new Date(`${to}T00:00:00`).getTime();
  return Math.round((b - a) / 86400000);
}

function formatDate(iso: string): string {
  return new Date(`${iso}T00:00:00`).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

// Name/currency resolution over the loaded accounts, bags and base-currency
// setting — purchase estimates carry neither account nor bag.
interface ContextLookup {
  accountName: (id?: number) => string;
  bagName: (id?: number) => string;
  /** The bag's parent account — bags are virtual partitions, so funding one
   *  always lands on the account the bag lives on (that account is implicit
   *  in the payload and must surface in every read the user sees). */
  bagAccountName: (id?: number) => string;
  currencyFor: (p: SchedulePayload) => string;
}

/** Human sentence describing the payload — used in the schedule list and the
 *  apply confirmation, so the user always sees what a click writes. */
function payloadSummary(p: SchedulePayload, lookup: ContextLookup): string {
  if (p.bag_id !== undefined) {
    return `${formatCents(p.amount_cents ?? 0, lookup.currencyFor(p))} into ${lookup.bagName(p.bag_id)} (${lookup.bagAccountName(p.bag_id)})`;
  }
  if (p.from_bag_id !== undefined && p.to_bag_id !== undefined) {
    return `${formatCents(p.amount_cents ?? 0, lookup.currencyFor(p))} from ${lookup.bagName(p.from_bag_id)} (${lookup.bagAccountName(p.from_bag_id)}) to ${lookup.bagName(p.to_bag_id)}`;
  }
  if (p.transaction_kind === 'expense' || p.transaction_kind === 'income') {
    return `${formatCents(p.amount_cents ?? 0, lookup.currencyFor(p))} ${p.transaction_kind} on ${lookup.accountName(p.account_id)}`;
  }
  if (p.estimated_cents !== undefined || (p.note !== undefined && p.account_id === undefined)) {
    return p.estimated_cents !== undefined
      ? `est. ${formatCents(p.estimated_cents, lookup.currencyFor(p))}`
      : 'checklist item';
  }
  return `${formatCents(p.amount_cents ?? 0, lookup.currencyFor(p))} from ${lookup.accountName(p.account_id)} to ${lookup.accountName(p.counter_account_id)}`;
}

/** The confirmation sentence for a confirm-first apply — names the exact
 *  money movement (or checklist tick) the click writes. */
function applyCopy(run: ScheduleRunView, lookup: ContextLookup): string {
  if (run.kind === 'purchase') {
    return `Mark “${run.action_name}” as bought?`;
  }
  const kindWord =
    run.kind === 'account_transfer'
      ? 'transfer'
      : run.schedule.transaction_kind === 'income'
        ? 'income entry'
        : 'expense entry';
  return `Record a ${kindWord}: ${payloadSummary(run.schedule, lookup)}?`;
}

export function SchedulesPage({ embedded = false }: { embedded?: boolean }) {
  const schedules = useAsync(() => schedulesApi.list(), []);
  const runs = useAsync(
    () =>
      schedulesApi.runs({ status: ['pending', 'applied', 'dismissed', 'skipped'], limit: 200 }),
    [],
  );
  const accounts = useAsync(() => accountsApi.list(), []);
  const bags = useAsync(() => bagsApi.overview(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  const baseCurrency = useAsync(() => settingsApi.getBaseCurrency(), []);

  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<ScheduledAction | null>(null);
  const [confirmApply, setConfirmApply] = useState<ScheduleRunView | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<ScheduledAction | null>(null);
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [showHistory, setShowHistory] = useState(false);

  const lookup = useMemo<ContextLookup>(() => {
    const accById = new Map((accounts.data ?? []).map((a) => [a.id, a]));
    const bagViews = bags.data?.accounts ?? [];
    const bagById = new Map(bagViews.flatMap((v) => v.bags).map((b) => [b.id, b]));
    // Bag → parent-account name: BagStatus rows carry account_id; the section
    // they sit in carries the account_name (bagAccountId keeps the link explicit).
    const bagAccountId = new Map(bagViews.flatMap((v) => v.bags.map((b) => [b.id, v.account_id] as const)));
    const bagCurrency = new Map(bagViews.flatMap((v) => v.bags.map((b) => [b.id, v.currency] as const)));
    return {
      accountName: (id) => (id !== undefined ? (accById.get(id)?.name ?? `account #${id}`) : '—'),
      bagName: (id) => (id !== undefined ? (bagById.get(id)?.name ?? `bag #${id}`) : '—'),
      bagAccountName: (id) => {
        if (id === undefined) return '—';
        const accId = bagAccountId.get(id);
        return accId !== undefined ? (accById.get(accId)?.name ?? `account #${accId}`) : 'unknown account';
      },
      currencyFor: (p) => {
        if (p.account_id !== undefined) return accById.get(p.account_id)?.currency ?? 'EUR';
        const bagId = p.bag_id ?? p.from_bag_id;
        if (bagId !== undefined) return bagCurrency.get(bagId) ?? 'EUR';
        return baseCurrency.data?.currency ?? 'EUR';
      },
    };
  }, [accounts.data, bags.data, baseCurrency.data]);

  const today = todayISO();
  const allRuns = runs.data ?? [];
  const todo = allRuns
    .filter((r) => r.status === 'pending')
    .sort((a, b) => a.due_date.localeCompare(b.due_date) || a.id - b.id);
  const historyRows = allRuns.filter((r) => r.status !== 'pending');
  const active = schedules.data ?? [];

  async function handleApplyConfirm(run: ScheduleRunView | null) {
    if (!run) return;
    setBusy(true);
    setPageError(null);
    try {
      await schedulesApi.applyRun(run.id);
      setConfirmApply(null);
      await runs.reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to apply.');
    } finally {
      setBusy(false);
    }
  }

  async function handleRunAction(run: ScheduleRunView, action: 'dismiss' | 'skip') {
    setBusy(true);
    setPageError(null);
    try {
      await (action === 'dismiss' ? schedulesApi.dismissRun(run.id) : schedulesApi.skipRun(run.id));
      await runs.reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to update.');
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setBusy(true);
    setPageError(null);
    try {
      await schedulesApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      setFormError(null);
      await schedules.reload();
      await runs.reload();
    } catch (err) {
      // A refused delete means history exists — keep the dialog open and put
      // the "archive instead" advice where the delete button sits.
      setFormError(err instanceof Error ? err.message : 'Failed to delete schedule.');
    } finally {
      setBusy(false);
    }
  }

  async function handleArchive(action: ScheduledAction) {
    setBusy(true);
    setPageError(null);
    try {
      await schedulesApi.archive(action.id);
      await schedules.reload();
      await runs.reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : 'Failed to archive schedule.');
    } finally {
      setBusy(false);
    }
  }

  function closeForm() {
    setShowForm(false);
    setEditing(null);
    setFormError(null);
  }

  if (schedules.loading || accounts.loading || bags.loading) return <Spinner />;
  if (schedules.error) return <ErrorMessage message={schedules.error.message} />;

  const openBags = (bags.data?.accounts ?? []).map((v) => v.bags).flat().filter((b) => b.status === 'open');

  const body = (
    <>
      {pageError && <ErrorMessage message={pageError} />}

      <Card title="To do">
        <div className="form-row">
          <Button variant="primary" onClick={() => { setFormError(null); setShowForm(true); }}>
            Add schedule
          </Button>
        </div>
        {todo.length === 0 ? (
          <p className="hint-text">
            Nothing due — the next occurrences appear here as their due dates arrive.
          </p>
        ) : (
          <div className="budget-list">
            {todo.map((run) => (
              <div key={run.id} className="budget-card">
                <div className="budget-head">
                  <span className="budget-dot" aria-hidden="true" />
                  <span className="budget-name">{run.action_name}</span>
                  <span className={run.due_date < today ? 'badge danger' : 'badge'}>
                    {dueLabel(run.due_date, today)}
                  </span>
                  <span className="budget-amounts">{formatDate(run.due_date)}</span>
                </div>
                <div className="budget-status">
                  {KIND_LABELS[run.kind]} · {payloadSummary(run.schedule, lookup)}
                </div>
                <div className="budget-actions">
                  {VIRTUAL_KINDS.includes(run.kind) ? (
                    <span className="stat-label">applies itself on the due date</span>
                  ) : (
                    <Button variant="primary" className="btn-sm" disabled={busy} onClick={() => setConfirmApply(run)}>
                      {run.kind === 'purchase' ? 'Done' : 'Apply'}
                    </Button>
                  )}
                  <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => handleRunAction(run, 'dismiss')}>
                    Dismiss
                  </Button>
                  <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => handleRunAction(run, 'skip')}>
                    Skip
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>

      <Card title="Schedules">
        {active.length === 0 ? (
          <EmptyState message="No scheduled movements yet — plan a bag funding, a rent transfer or a recurring expense above." />
        ) : (
          <div className="budget-list">
            {active.map((s) => (
              <div key={s.id} className="budget-card">
                <div className="budget-head">
                  <span className="budget-dot" aria-hidden="true" />
                  <span className="budget-name">{s.name}</span>
                  <span className="badge">{RECURRENCE_LABELS[s.recurrence]}</span>
                  <span className="budget-amounts">{formatDate(s.start_date)}</span>
                </div>
                <div className="budget-status">
                  {KIND_LABELS[s.kind]} · {payloadSummary(s.payload, lookup)}
                  {s.recurrence !== 'once' && s.day_of_month
                    ? ` · day ${s.day_of_month}${s.recurrence === 'yearly' && s.month_of_year ? ` of month ${s.month_of_year}` : ''}`
                    : ''}
                </div>
                <div className="budget-actions">
                  <Button variant="secondary" className="btn-sm" onClick={() => { setFormError(null); setEditing(s); }}>
                    Edit
                  </Button>
                  <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => handleArchive(s)}>
                    Archive
                  </Button>
                  <Button variant="danger" className="btn-sm" onClick={() => { setFormError(null); setConfirmDelete(s); }}>
                    Delete
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
        <p className="hint-text">
          Editing a schedule regenerates its future occurrences; anything already
          applied stays put. Deleting is only allowed while nothing was ever applied.
        </p>
        {historyRows.length > 0 && (
          <div>
            <Button variant="ghost" onClick={() => setShowHistory(!showHistory)}>
              {showHistory ? 'Hide history' : `Show history (${historyRows.length})`}
            </Button>
            {showHistory && (
              <div className="budget-list">
                {[...historyRows]
                  .sort((a, b) => b.due_date.localeCompare(a.due_date))
                  .map((run) => (
                    <div key={run.id} className="budget-card">
                      <div className="budget-head">
                        <span className="budget-dot closed" aria-hidden="true" />
                        <span className="budget-name">{run.action_name}</span>
                        <span className="badge">{STATUS_LABELS[run.status]}</span>
                        <span className="budget-amounts">{formatDate(run.due_date)}</span>
                      </div>
                      <div className="budget-status">
                        {KIND_LABELS[run.kind]} · {payloadSummary(run.schedule, lookup)}
                        {run.result_type === 'transaction' && ' · transaction written'}
                        {run.result_type === 'allocation' && ' · bag allocation written'}
                      </div>
                    </div>
                  ))}
              </div>
            )}
          </div>
        )}
      </Card>

      {(showForm || editing) && (
        <ScheduleFormDialog
          initial={editing}
          accounts={accounts.data ?? []}
          openBags={openBags}
          categories={categories.data ?? []}
          busy={busy}
          error={formError}
          onCancel={closeForm}
          onSubmit={async (input) => {
            setBusy(true);
            setFormError(null);
            try {
              if (editing) {
                await schedulesApi.update(editing.id, input);
              } else {
                await schedulesApi.create(input);
              }
              closeForm();
              await schedules.reload();
              await runs.reload();
            } catch (err) {
              setFormError(err instanceof Error ? err.message : 'Failed to save schedule.');
            } finally {
              setBusy(false);
            }
          }}
        />
      )}

      {confirmApply && (
        <Dialog title={confirmApply.kind === 'purchase' ? 'Mark as bought' : 'Apply now'}>
          <p className="hint-text">{applyCopy(confirmApply, lookup)}</p>
          <p className="hint-text">
            Dated {formatDate(confirmApply.due_date)}. The written transaction can be
            corrected or removed in Transactions afterwards.
          </p>
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => setConfirmApply(null)} disabled={busy}>
              Cancel
            </Button>
            <Button variant="primary" onClick={() => void handleApplyConfirm(confirmApply)} disabled={busy}>
              Confirm
            </Button>
          </div>
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title="Delete schedule?">
          <p className="hint-text">
            Delete “{confirmDelete.name}”? Its pending occurrences disappear with it.
            If it ever applied anything the delete is refused — archive keeps the
            history instead.
          </p>
          {formError && <ErrorMessage message={formError} />}
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => { setConfirmDelete(null); setFormError(null); }} disabled={busy}>
              Cancel
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busy}>
              Delete
            </Button>
          </div>
        </Dialog>
      )}
    </>
  );

  if (embedded) return body;
  return (
    <div className="page">
      <h2 className="page-title">Scheduled actions</h2>
      {body}
    </div>
  );
}

// --- create / edit form ------------------------------------------------------

interface ScheduleFormDialogProps {
  initial: ScheduledAction | null;
  accounts: Account[];
  openBags: BagStatus[];
  categories: Category[];
  busy: boolean;
  error: string | null;
  onCancel: () => void;
  onSubmit: (input: ScheduledActionInput) => Promise<void>;
}

function ScheduleFormDialog({
  initial,
  accounts,
  openBags,
  categories,
  busy,
  error,
  onCancel,
  onSubmit,
}: ScheduleFormDialogProps) {
  const [name, setName] = useState(initial?.name ?? '');
  const [kind, setKind] = useState<ScheduleKind>(initial?.kind ?? 'bag_funding');
  const [recurrence, setRecurrence] = useState<ScheduleRecurrence>(initial?.recurrence ?? 'once');
  const [startDate, setStartDate] = useState(initial?.start_date ?? todayISO());
  const [dayOfMonth, setDayOfMonth] = useState(initial?.day_of_month ? String(initial.day_of_month) : '1');
  const [monthOfYear, setMonthOfYear] = useState(initial?.month_of_year ? String(initial.month_of_year) : '1');

  // Kind-payload state, read back into the payload on submit. Empty money
  // strings mean "unset" only where the field is optional (purchase estimate).
  const p0: SchedulePayload = initial?.payload ?? {};
  const [bagId, setBagId] = useState(p0.bag_id ? String(p0.bag_id) : '');
  const [fromBagId, setFromBagId] = useState(p0.from_bag_id ? String(p0.from_bag_id) : '');
  const [toBagId, setToBagId] = useState(p0.to_bag_id ? String(p0.to_bag_id) : '');
  const [accountId, setAccountId] = useState(p0.account_id ? String(p0.account_id) : '');
  const [counterId, setCounterId] = useState(p0.counter_account_id ? String(p0.counter_account_id) : '');
  const [txKind, setTxKind] = useState<'expense' | 'income'>(p0.transaction_kind ?? 'expense');
  const [categoryId, setCategoryId] = useState(p0.category_id ? String(p0.category_id) : '');
  const [amount, setAmount] = useState(p0.amount_cents ? centsToInput(p0.amount_cents) : '');
  const [estimated, setEstimated] = useState(p0.estimated_cents ? centsToInput(p0.estimated_cents) : '');
  const [description, setDescription] = useState(p0.description ?? '');
  const [note, setNote] = useState(p0.note ?? '');
  const [validation, setValidation] = useState<string | null>(null);

  const fromBag = openBags.find((b) => String(b.id) === fromBagId);
  const sameAccountBags = openBags.filter((b) => !fromBag || (b.account_id === fromBag.account_id && b.id !== fromBag.id));

  function validate(): ScheduledActionInput | null {
    if (!name.trim()) { setValidation('Enter a name.'); return null; }
    if (!startDate) { setValidation('Enter a start date.'); return null; }
    const cents = dollarsToCents(amount);
    const purchaseCents = dollarsToCents(estimated);

    let payload: SchedulePayload = {};

    if (kind === 'bag_funding') {
      if (!bagId) { setValidation('Choose the bag to fund.'); return null; }
      if (!Number.isFinite(cents) || cents <= 0) { setValidation('Enter a positive amount.'); return null; }
      payload = { bag_id: Number(bagId), amount_cents: cents };
    } else if (kind === 'bag_rebalance') {
      if (!fromBagId || !toBagId) { setValidation('Choose the source and destination bag.'); return null; }
      if (fromBagId === toBagId) { setValidation('Source and destination must differ.'); return null; }
      const to = openBags.find((b) => String(b.id) === toBagId);
      if (fromBag && to && fromBag.account_id !== to.account_id) {
        setValidation('Both bags must live on the same account.');
        return null;
      }
      if (!Number.isFinite(cents) || cents <= 0) { setValidation('Enter a positive amount.'); return null; }
      payload = { from_bag_id: Number(fromBagId), to_bag_id: Number(toBagId), amount_cents: cents };
    } else if (kind === 'account_transfer') {
      if (!accountId || !counterId) { setValidation('Choose the source and destination account.'); return null; }
      if (accountId === counterId) { setValidation('Source and destination accounts must differ.'); return null; }
      if (!Number.isFinite(cents) || cents <= 0) { setValidation('Enter a positive amount.'); return null; }
      payload = { account_id: Number(accountId), counter_account_id: Number(counterId), amount_cents: cents };
    } else if (kind === 'transaction') {
      if (!accountId) { setValidation('Choose the account the transaction runs on.'); return null; }
      if (!Number.isFinite(cents) || cents <= 0) { setValidation('Enter a positive amount.'); return null; }
      payload = {
        account_id: Number(accountId),
        transaction_kind: txKind,
        amount_cents: cents,
        category_id: categoryId ? Number(categoryId) : undefined,
        description: description.trim(),
      };
    } else {
      // purchase: the checklist item — estimate optional, note optional.
      if (estimated && (!Number.isFinite(purchaseCents) || purchaseCents <= 0)) {
        setValidation('Estimated amount must be a positive number.');
        return null;
      }
      payload = {
        estimated_cents: estimated && purchaseCents > 0 ? purchaseCents : undefined,
        note: note.trim(),
      };
    }

    const day = Number(dayOfMonth);
    const month = Number(monthOfYear);
    if (recurrence === 'monthly' || recurrence === 'yearly') {
      if (!Number.isInteger(day) || day < 1 || day > 28) {
        setValidation('Day of month must be 1–28 so short months never skip.');
        return null;
      }
    }
    if (recurrence === 'yearly' && (!Number.isInteger(month) || month < 1 || month > 12)) {
      setValidation('Month of year must be 1–12.');
      return null;
    }

    return {
      name: name.trim(),
      kind,
      payload,
      recurrence,
      start_date: startDate,
      day_of_month: recurrence === 'monthly' || recurrence === 'yearly' ? day : undefined,
      month_of_year: recurrence === 'yearly' ? month : undefined,
    };
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setValidation(null);
    const input = validate();
    if (!input) return;
    await onSubmit(input);
  }

  return (
    <Dialog title={initial ? 'Edit schedule' : 'Add schedule'} wide>
      <form className="form-row" onSubmit={handleSubmit}>
        <input
          placeholder="Schedule name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={100}
          aria-label="Schedule name"
          required
        />
        <select value={kind} onChange={(e) => setKind(e.target.value as ScheduleKind)} aria-label="Schedule kind">
          <option value="bag_funding">Fund bag</option>
          <option value="bag_rebalance">Rebalance bags</option>
          <option value="account_transfer">Account transfer</option>
          <option value="transaction">Transaction</option>
          <option value="purchase">Purchase (checklist)</option>
        </select>
        <select
          value={recurrence}
          onChange={(e) => setRecurrence(e.target.value as ScheduleRecurrence)}
          aria-label="Recurrence"
        >
          <option value="once">Once</option>
          <option value="monthly">Monthly</option>
          <option value="yearly">Yearly</option>
        </select>
        <input
          type="date"
          value={startDate}
          onChange={(e) => setStartDate(e.target.value)}
          aria-label="Start date"
          required
        />
        {(recurrence === 'monthly' || recurrence === 'yearly') && (
          <select value={dayOfMonth} onChange={(e) => setDayOfMonth(e.target.value)} aria-label="Day of month">
            {Array.from({ length: 28 }, (_, i) => i + 1).map((d) => (
              <option key={d} value={d}>Day {d}</option>
            ))}
          </select>
        )}
        {recurrence === 'yearly' && (
          <select value={monthOfYear} onChange={(e) => setMonthOfYear(e.target.value)} aria-label="Month of year">
            {Array.from({ length: 12 }, (_, i) => i + 1).map((m) => (
              <option key={m} value={m}>
                {new Date(2000, m - 1, 1).toLocaleDateString('en-US', { month: 'long' })}
              </option>
            ))}
          </select>
        )}

        {kind === 'bag_funding' && (
          <>
            <select value={bagId} onChange={(e) => setBagId(e.target.value)} aria-label="Bag to fund" required>
              <option value="">Fund which bag…</option>
              {openBags.map((b) => (
                <option key={b.id} value={b.id}>{b.name} · {b.account_name}</option>
              ))}
            </select>
            {bagId && (
              <p className="hint-text">
                {bagAccountLabel(bagId, openBags)} — funding claims part of that account's
                balance inside the bag; the account's stored balance never changes.
              </p>
            )}
          </>
        )}
        {kind === 'bag_rebalance' && (
          <>
            <select value={fromBagId} onChange={(e) => { setFromBagId(e.target.value); setToBagId(''); }} aria-label="Source bag" required>
              <option value="">From bag…</option>
              {openBags.map((b) => (
                <option key={b.id} value={b.id}>{b.name} · {b.account_name}</option>
              ))}
            </select>
            <select value={toBagId} onChange={(e) => setToBagId(e.target.value)} aria-label="Destination bag" required disabled={!fromBag}>
              <option value="">To bag…</option>
              {sameAccountBags.map((b) => (
                <option key={b.id} value={b.id}>{b.name} · {b.account_name}</option>
              ))}
            </select>
            {fromBagId && toBagId && (
              <p className="hint-text">
                Both bags live on {bagAccountLabel(fromBagId, openBags)} — a rebalance
                reshuffles the partition, the account's stored balance never changes.
              </p>
            )}
          </>
        )}
        {(kind === 'account_transfer' || kind === 'transaction') && (
          <select
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
            aria-label="Account"
            required
          >
            <option value="">{kind === 'account_transfer' ? 'From account…' : 'On account…'}</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        )}
        {kind === 'account_transfer' && (
          <select value={counterId} onChange={(e) => setCounterId(e.target.value)} aria-label="Destination account" required>
            <option value="">To account…</option>
            {accounts.filter((a) => String(a.id) !== accountId).map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        )}
        {kind === 'transaction' && (
          <select value={txKind} onChange={(e) => setTxKind(e.target.value as 'expense' | 'income')} aria-label="Transaction type">
            <option value="expense">Expense</option>
            <option value="income">Income</option>
          </select>
        )}
        {kind === 'transaction' && (
          <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} aria-label="Category">
            <option value="">No category</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
        )}
        {kind !== 'purchase' && (
          <input
            placeholder="Amount"
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            required
          />
        )}
        {kind === 'transaction' && (
          <input
            placeholder="Description (blank = schedule name)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={200}
          />
        )}
        {kind === 'purchase' && (
          <>
            <input
              placeholder="Estimated cost (optional)"
              inputMode="decimal"
              value={estimated}
              onChange={(e) => setEstimated(e.target.value)}
            />
            <input
              placeholder="Note (e.g. check sizes first)"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              maxLength={200}
            />
          </>
        )}

        <p className="hint-text">
          {kind === 'bag_funding' || kind === 'bag_rebalance'
            ? 'Applied automatically on the due date — no click needed.'
            : kind === 'purchase'
              ? 'A purchase stays a checklist item: applying it ticks it off and writes nothing.'
              : 'Confirm-first: the occurrence waits in your to-do list until one click applies it.'}
          {' '}Editing affects future occurrences only.
        </p>
        {validation && <ErrorMessage message={validation} />}
        {error && <ErrorMessage message={error} />}
        <div className="dialog-actions">
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" disabled={busy}>
            {initial ? 'Save' : 'Create'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

/** Input-safe cents rendering for form prefill ("12.50"). */
function centsToInput(cents: number): string {
  return (cents / 100).toFixed(2);
}

/** "the bag lives on <account>" label for the form hints. */
function bagAccountLabel(bagId: string, openBags: BagStatus[]): string {
  const bag = openBags.find((b) => String(b.id) === bagId);
  return bag ? `The bag lives on ${bag.account_name}` : '';
}