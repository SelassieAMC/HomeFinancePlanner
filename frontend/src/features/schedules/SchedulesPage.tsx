import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { useAsync } from '../../hooks/useAsync';
import { schedulesApi } from '../../api/schedules';
import { accountsApi } from '../../api/accounts';
import { bagsApi } from '../../api/bags';
import { categoriesApi } from '../../api/categories';
import { settingsApi } from '../../api/settings';
import { formatCents } from '../../lib/money';
import { todayISO } from '../../lib/date';
import type {
  Account,
  BagStatus,
  Category,
  Payable,
  PayablesTotal,
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
  IconButton,
  MoneyInput,
  IconArchive,
  IconChevronLeft,
  IconChevronRight,
  IconEdit,
  IconTrash,
} from '../../components/ui';

// Scheduled actions are the unified scheduler: virtual kinds (bag funding /
// bag rebalancing) are applied by the system on their due date — the catch-up
// runs on every read, so there is nothing to click — while real movements
// (transfers, transactions) and purchases are confirm-first: they sit in the
// to-do feed until one click applies, dismisses or skips them. Edits affect
// future occurrences only; history rows are immutable.

// Translation keys per enum value — the labels themselves resolve at render
// time through t().
const KIND_KEYS: Record<ScheduleKind, string> = {
  bag_funding: 'kinds.bag_funding',
  bag_rebalance: 'kinds.bag_rebalance',
  account_transfer: 'kinds.account_transfer',
  transaction: 'kinds.transaction',
  purchase: 'kinds.purchase',
};

// Virtual kinds never offer Apply: auto-applied on the due date (they only
// write bag ledger rows, never a stored account balance).
const VIRTUAL_KINDS: readonly ScheduleKind[] = ['bag_funding', 'bag_rebalance'];

const RECURRENCE_KEYS: Record<ScheduleRecurrence, string> = {
  once: 'recurrence.once',
  monthly: 'recurrence.monthly',
  yearly: 'recurrence.yearly',
};

const STATUS_KEYS: Record<ScheduleRunStatus, string> = {
  pending: 'status.pending',
  applied: 'status.applied',
  dismissed: 'status.dismissed',
  skipped: 'status.skipped',
};

/** Badge showing how far a due date is away — translated at render time via
 *  the count keys (`due.overdue_one`/`due.overdue_other` …). */
interface DueLabel {
  key: string;
  count?: number;
}

function dueLabel(due: string, today: string): DueLabel {
  if (due < today) {
    return { key: 'due.overdue', count: daysBetween(due, today) };
  }
  if (due === today) return { key: 'due.today' };
  return { key: 'due.in', count: daysBetween(today, due) };
}

/** Whole days from `from` to `to` (string dates, no timezone traps). */
function daysBetween(from: string, to: string): number {
  const a = new Date(`${from}T00:00:00`).getTime();
  const b = new Date(`${to}T00:00:00`).getTime();
  return Math.round((b - a) / 86400000);
}

function formatDate(iso: string): string {
  return new Date(`${iso}T00:00:00`).toLocaleDateString(intlLocale(i18n.language), {
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

export function SchedulesPage({ embedded = false }: { embedded?: boolean }) {
  const { t } = useTranslation('schedules');
  const schedules = useAsync(() => schedulesApi.list(), []);
  const runs = useAsync(
    () =>
      schedulesApi.runs({ status: ['pending', 'applied', 'dismissed', 'skipped'], limit: 200 }),
    [],
  );
  // The occurrences register: one month at a time (MoneyWiz-style paging).
  // An empty string means the server's current month; the arrows shift it.
  const [registerMonth, setRegisterMonth] = useState('');
  const payables = useAsync(() => schedulesApi.payables(registerMonth || undefined), [registerMonth]);
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
      accountName: (id) =>
        id !== undefined ? (accById.get(id)?.name ?? t('fallbacks.account', { id })) : '—',
      bagName: (id) =>
        id !== undefined ? (bagById.get(id)?.name ?? t('fallbacks.bag', { id })) : '—',
      bagAccountName: (id) => {
        if (id === undefined) return '—';
        const accId = bagAccountId.get(id);
        return accId !== undefined
          ? (accById.get(accId)?.name ?? t('fallbacks.account', { id: accId }))
          : t('fallbacks.unknown_account');
      },
      currencyFor: (p) => {
        if (p.account_id !== undefined) return accById.get(p.account_id)?.currency ?? 'EUR';
        const bagId = p.bag_id ?? p.from_bag_id;
        if (bagId !== undefined) return bagCurrency.get(bagId) ?? 'EUR';
        return baseCurrency.data?.currency ?? 'EUR';
      },
    };
  }, [accounts.data, bags.data, baseCurrency.data, t]);

  const today = todayISO();
  const allRuns = runs.data ?? [];
  const historyRows = allRuns.filter((r) => r.status !== 'pending');
  const active = schedules.data ?? [];

  /** Human sentence describing the payload — used in the schedule list and the
   *  apply confirmation, so the user always sees what a click writes. */
  function payloadSummary(p: SchedulePayload, lookup: ContextLookup): string {
    if (p.bag_id !== undefined) {
      return t('summary.into', {
        amount: formatCents(p.amount_cents ?? 0, lookup.currencyFor(p)),
        bag: lookup.bagName(p.bag_id),
        account: lookup.bagAccountName(p.bag_id),
      });
    }
    if (p.from_bag_id !== undefined && p.to_bag_id !== undefined) {
      return t('summary.bags', {
        amount: formatCents(p.amount_cents ?? 0, lookup.currencyFor(p)),
        from: lookup.bagName(p.from_bag_id),
        fromAccount: lookup.bagAccountName(p.from_bag_id),
        to: lookup.bagName(p.to_bag_id),
      });
    }
    if (p.transaction_kind === 'expense' || p.transaction_kind === 'income') {
      return t('summary.tx', {
        amount: formatCents(p.amount_cents ?? 0, lookup.currencyFor(p)),
        kind: t(p.transaction_kind === 'expense' ? 'summary.expense' : 'summary.income'),
        account: lookup.accountName(p.account_id),
      });
    }
    if (p.estimated_cents !== undefined || (p.note !== undefined && p.account_id === undefined)) {
      return p.estimated_cents !== undefined
        ? t('summary.est', { amount: formatCents(p.estimated_cents, lookup.currencyFor(p)) })
        : t('summary.checklist');
    }
    return t('summary.transfer', {
      amount: formatCents(p.amount_cents ?? 0, lookup.currencyFor(p)),
      from: lookup.accountName(p.account_id),
      to: lookup.accountName(p.counter_account_id),
    });
  }

  /** The confirmation sentence for a confirm-first apply — names the exact
   *  money movement (or checklist tick) the click writes. */
  function applyCopy(run: ScheduleRunView, lookup: ContextLookup): string {
    if (run.kind === 'purchase') {
      return t('apply.mark_bought', { name: run.action_name });
    }
    const kindWord =
      run.kind === 'account_transfer'
        ? t('apply.kind_transfer')
        : run.schedule.transaction_kind === 'income'
          ? t('apply.kind_income')
          : t('apply.kind_expense');
    return t('apply.record', { kind: kindWord, summary: payloadSummary(run.schedule, lookup) });
  }

  /** Any state change that touches runs also repaints the payables card. */
  async function reloadRuns() {
    await Promise.all([runs.reload(), payables.reload()]);
  }

  async function handleApplyConfirm(run: ScheduleRunView | null) {
    if (!run) return;
    setBusy(true);
    setPageError(null);
    try {
      await schedulesApi.applyRun(run.id);
      setConfirmApply(null);
      await reloadRuns();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.apply'));
    } finally {
      setBusy(false);
    }
  }

  async function handleRunAction(run: ScheduleRunView, action: 'dismiss' | 'skip') {
    setBusy(true);
    setPageError(null);
    try {
      await (action === 'dismiss' ? schedulesApi.dismissRun(run.id) : schedulesApi.skipRun(run.id));
      await reloadRuns();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.update'));
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
      await reloadRuns();
    } catch (err) {
      // Delete always succeeds with real access errors only — keep the
      // dialog open and put the message where the button sits.
      setFormError(err instanceof Error ? err.message : t('errors.delete'));
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
      await reloadRuns();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.archive'));
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

  /** Bridges from a payables-report occurrence to the shapes the existing
   *  handlers work with (apply/dismiss/skip target runs — the item carries
   *  the run id when its occurrence is materialized). */
  function runOf(it: Payable): ScheduleRunView {
    return {
      id: it.run_id ?? 0,
      action_id: it.action_id,
      due_date: it.due_date,
      status: it.status === 'projected' ? 'pending' : it.status,
      action_name: it.name,
      kind: it.kind,
      schedule: it.schedule,
      created_at: '',
      updated_at: '',
    };
  }

  /** Bridges to the plan row (edit/delete from the occurrence line). */
  function planOf(it: Payable): ScheduledAction {
    return {
      id: it.action_id,
      name: it.name,
      kind: it.kind,
      payload: it.schedule,
      recurrence: it.recurrence,
      start_date: it.start_date,
      day_of_month: it.day_of_month,
      month_of_year: it.month_of_year,
      auto_apply: it.auto_apply,
      archived: false,
      created_at: '',
      updated_at: '',
    };
  }

  const body = (
    <>
      {pageError && <ErrorMessage message={pageError} />}

      {(active.length > 0 || (payables.data?.items ?? []).length > 0 || historyRows.length > 0) && (
        <PayablesCard
          month={payables.data?.month ?? (registerMonth || today.slice(0, 7))}
          items={payables.data?.items ?? []}
          totals={payables.data?.totals ?? []}
          today={today}
          busy={payables.loading || busy}
          describe={(p) => payloadSummary(p, lookup)}
          historyRows={historyRows}
          showHistory={showHistory}
          onToggleHistory={() => setShowHistory(!showHistory)}
          onAdd={() => { setFormError(null); setShowForm(true); }}
          onApply={(it) => setConfirmApply(runOf(it))}
          onRunAction={(it, action) => handleRunAction(runOf(it), action)}
          onEdit={(it) => { setFormError(null); setEditing(planOf(it)); }}
          onArchive={(it) => handleArchive(planOf(it))}
          onDelete={(it) => setConfirmDelete(planOf(it))}
          onShift={(delta) => setRegisterMonth(shiftMonth(payables.data?.month ?? today.slice(0, 7), delta))}
        />
      )}

      {(showForm || editing) && (
        <ScheduleFormDialog
          initial={editing}
          accounts={accounts.data ?? []}
          openBags={openBags}
          categories={categories.data ?? []}
          baseCurrency={baseCurrency.data?.currency ?? 'EUR'}
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
              // A fresh schedule lands in ITS start month — flip the
              // register there so the user sees the row they just created.
              setRegisterMonth(input.start_date.slice(0, 7));
              await schedules.reload();
              await reloadRuns();
            } catch (err) {
              setFormError(err instanceof Error ? err.message : t('errors.save'));
            } finally {
              setBusy(false);
            }
          }}
        />
      )}

      {confirmApply && (
        <Dialog title={confirmApply.kind === 'purchase' ? t('apply.title_bought') : t('apply.title_apply')}>
          <p className="hint-text">{applyCopy(confirmApply, lookup)}</p>
          {/* Early payment records at today's real date (MoneyWiz rule: the
              due date is a deadline, not the record date). */}
          <p className="hint-text">
            {confirmApply.due_date > today
              ? t('apply.dated_early', { date: formatDate(today), due: formatDate(confirmApply.due_date) })
              : t('apply.dated', { date: formatDate(confirmApply.due_date) })}
          </p>
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => setConfirmApply(null)} disabled={busy}>
              {t('apply.cancel')}
            </Button>
            <Button variant="primary" onClick={() => void handleApplyConfirm(confirmApply)} disabled={busy}>
              {t('apply.confirm')}
            </Button>
          </div>
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={t('delete.title')}>
          <p className="hint-text">{t('delete.body', { name: confirmDelete.name })}</p>
          {formError && <ErrorMessage message={formError} />}
          <div className="dialog-actions">
            <Button variant="ghost" onClick={() => { setConfirmDelete(null); setFormError(null); }} disabled={busy}>
              {t('delete.cancel')}
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busy}>
              {t('delete.confirm')}
            </Button>
          </div>
        </Dialog>
      )}
    </>
  );

  if (embedded) return body;
  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>
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
  baseCurrency: string;
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
  baseCurrency,
  busy,
  error,
  onCancel,
  onSubmit,
}: ScheduleFormDialogProps) {
  const { t } = useTranslation('schedules');
  const [name, setName] = useState(initial?.name ?? '');
  const [kind, setKind] = useState<ScheduleKind>(initial?.kind ?? 'bag_funding');
  const [recurrence, setRecurrence] = useState<ScheduleRecurrence>(initial?.recurrence ?? 'once');
  const [startDate, setStartDate] = useState(initial?.start_date ?? todayISO());
  const [monthOfYear, setMonthOfYear] = useState(initial?.month_of_year ? String(initial.month_of_year) : '1');

  // Kind-payload state, read back into the payload on submit. Money rides as
  // committed cents (MoneyInput formats and parses; null = untouched).
  const p0: SchedulePayload = initial?.payload ?? {};
  const [bagId, setBagId] = useState(p0.bag_id && initial?.kind === 'bag_funding' ? String(p0.bag_id) : '');
  const [fundAccountId, setFundAccountId] = useState<string>(
    p0.bag_id && initial?.kind === 'bag_funding'
      ? String(openBags.find((b) => String(b.id) === String(p0.bag_id))?.account_id ?? '')
      : '',
  );
  const [fromBagId, setFromBagId] = useState(p0.from_bag_id ? String(p0.from_bag_id) : '');
  const [toBagId, setToBagId] = useState(p0.to_bag_id ? String(p0.to_bag_id) : '');
  const [accountId, setAccountId] = useState(p0.account_id ? String(p0.account_id) : '');
  const [counterId, setCounterId] = useState(p0.counter_account_id ? String(p0.counter_account_id) : '');
  const [txKind, setTxKind] = useState<'expense' | 'income'>(p0.transaction_kind ?? 'expense');
  // The optional "From bag" tag of a scheduled expense (only when its
  // account has open bags — otherwise no selector renders).
  const [txBagId, setTxBagId] = useState(p0.bag_id && initial?.kind === 'transaction' ? String(p0.bag_id) : '');
  const [categoryId, setCategoryId] = useState(p0.category_id ? String(p0.category_id) : '');
  const [amountCents, setAmountCents] = useState<number | null>(p0.amount_cents ?? null);
  const [estimateCents, setEstimateCents] = useState<number | null>(p0.estimated_cents ?? null);
  const [description, setDescription] = useState(p0.description ?? '');
  const [note, setNote] = useState(p0.note ?? '');
  // The execution mode (0046): virtual kinds ALWAYS run themselves (no
  // checkbox); real kinds default manual — the red alert until confirmed.
  const [autoApply, setAutoApply] = useState(initial?.auto_apply ?? false);
  const [validation, setValidation] = useState<string | null>(null);

  const fromBag = openBags.find((b) => String(b.id) === fromBagId);
  const sameAccountBags = openBags.filter((b) => !fromBag || (b.account_id === fromBag.account_id && b.id !== fromBag.id));
  // Account-first funding: the money is reserved on ONE account's bags;
  // only accounts that actually have open bags are selectable.
  const fundAccountBags = openBags.filter((b) => String(b.account_id) === fundAccountId);
  const fundingAccounts = accounts.filter((a) => openBags.some((b) => String(b.account_id) === String(a.id)));
  // The scheduled expense's account owns its optional bag.
  const accountBags = openBags.filter((b) => String(b.account_id) === accountId);

  /** Currency the amount field speaks: the bag's account for bag kinds, the
   *  account for transfer/transaction kinds, the base for purchase. */
  function amountCurrency(): string {
    if (kind === 'purchase') return baseCurrency;
    if (kind === 'bag_funding') {
      const b = openBags.find((bg) => String(bg.id) === bagId);
      if (b) return tCurrency(b.account_id);
    }
    if (kind === 'bag_rebalance' && fromBagId) {
      const b = openBags.find((bg) => String(bg.id) === fromBagId);
      if (b) return tCurrency(b.account_id);
    }
    return tCurrency(Number(accountId) || undefined);
  }

  function tCurrency(accountIdLocal?: number): string {
    if (accountIdLocal === undefined) return baseCurrency;
    return accounts.find((a) => a.id === accountIdLocal)?.currency ?? baseCurrency;
  }

  /** "the bag lives on <account>" label for the form hints. */
  function bagAccountLabel(bagIdLocal: string, openBagsLocal: BagStatus[]): string {
    const bag = openBagsLocal.find((b) => String(b.id) === bagIdLocal);
    return bag ? t('form.lives_on', { account: bag.account_name }) : '';
  }

  function validate(): ScheduledActionInput | null {
    if (!name.trim()) { setValidation(t('validation.need_name')); return null; }
    if (!startDate) { setValidation(t('validation.need_start_date')); return null; }

    let payload: SchedulePayload = {};

    if (kind === 'bag_funding') {
      if (!bagId) { setValidation(t('validation.need_bag')); return null; }
      if (amountCents === null || amountCents <= 0) { setValidation(t('validation.positive_amount')); return null; }
      payload = { bag_id: Number(bagId), amount_cents: amountCents };
    } else if (kind === 'bag_rebalance') {
      if (!fromBagId || !toBagId) { setValidation(t('validation.need_bags')); return null; }
      if (fromBagId === toBagId) { setValidation(t('validation.bags_differ')); return null; }
      const to = openBags.find((b) => String(b.id) === toBagId);
      if (fromBag && to && fromBag.account_id !== to.account_id) {
        setValidation(t('validation.same_account'));
        return null;
      }
      if (amountCents === null || amountCents <= 0) { setValidation(t('validation.positive_amount')); return null; }
      payload = { from_bag_id: Number(fromBagId), to_bag_id: Number(toBagId), amount_cents: amountCents };
    } else if (kind === 'account_transfer') {
      if (!accountId || !counterId) { setValidation(t('validation.need_accounts')); return null; }
      if (accountId === counterId) { setValidation(t('validation.accounts_differ')); return null; }
      if (amountCents === null || amountCents <= 0) { setValidation(t('validation.positive_amount')); return null; }
      payload = { account_id: Number(accountId), counter_account_id: Number(counterId), amount_cents: amountCents };
    } else if (kind === 'transaction') {
      if (!accountId) { setValidation(t('validation.need_account')); return null; }
      if (amountCents === null || amountCents <= 0) { setValidation(t('validation.positive_amount')); return null; }
      payload = {
        account_id: Number(accountId),
        transaction_kind: txKind,
        amount_cents: amountCents,
        category_id: categoryId ? Number(categoryId) : undefined,
        // The optional bag tag rides only on expenses (backend mirrors).
        bag_id: txKind === 'expense' && txBagId ? Number(txBagId) : undefined,
        description: description.trim(),
      };
    } else {
      // purchase: the checklist item — estimate optional, note optional.
      if (estimateCents !== null && estimateCents <= 0) {
        setValidation(t('validation.estimate_positive'));
        return null;
      }
      payload = {
        estimated_cents: estimateCents !== null && estimateCents > 0 ? estimateCents : undefined,
        note: note.trim(),
      };
    }

    const month = Number(monthOfYear);
    if (recurrence === 'yearly' && (!Number.isInteger(month) || month < 1 || month > 12)) {
      setValidation(t('validation.month_range'));
      return null;
    }

    // No day_of_month here: the start date IS the repeating pattern — the
    // backend derives the day (clamped to the month's last day when shorter).
    return {
      name: name.trim(),
      kind,
      payload,
      recurrence,
      start_date: startDate,
      month_of_year: recurrence === 'yearly' ? month : undefined,
      auto_apply: autoApply,
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
    <Dialog title={initial ? t('form.edit_title') : t('form.add_title')} wide>
      <form className="form-row" onSubmit={handleSubmit}>
        <input
          placeholder={t('form.name_placeholder')}
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={100}
          aria-label={t('form.name_aria')}
          required
        />
        <select value={kind} onChange={(e) => setKind(e.target.value as ScheduleKind)} aria-label={t('form.kind_aria')}>
          <option value="bag_funding">{t('kinds.bag_funding')}</option>
          <option value="bag_rebalance">{t('kinds.bag_rebalance')}</option>
          <option value="account_transfer">{t('kinds.account_transfer')}</option>
          <option value="transaction">{t('kinds.transaction')}</option>
          <option value="purchase">{t('kinds.purchase_checklist')}</option>
        </select>
        <select
          value={recurrence}
          onChange={(e) => setRecurrence(e.target.value as ScheduleRecurrence)}
          aria-label={t('form.recurrence_aria')}
        >
          <option value="once">{t('recurrence.once')}</option>
          <option value="monthly">{t('recurrence.monthly')}</option>
          <option value="yearly">{t('recurrence.yearly')}</option>
        </select>
        <input
          type="date"
          value={startDate}
          onChange={(e) => setStartDate(e.target.value)}
          aria-label={t('form.start_aria')}
          required
        />
        {(recurrence === 'monthly' || recurrence === 'yearly') && startDate.length === 10 && (
          <p className="hint-text">
            {t('form.day_derived_hint', { day: Number(startDate.slice(8, 10)) })}
          </p>
        )}

        {kind !== 'bag_funding' && kind !== 'bag_rebalance' && (
          <label className="default-radio">
            {t('form.auto_apply_label')}
            <input
              type="checkbox"
              checked={autoApply}
              onChange={(e) => setAutoApply(e.target.checked)}
            />
          </label>
        )}
        {kind !== 'bag_funding' && kind !== 'bag_rebalance' && autoApply && (
          <p className="hint-text">{t('form.auto_apply_hint')}</p>
        )}
        {recurrence === 'yearly' && (
          <select value={monthOfYear} onChange={(e) => setMonthOfYear(e.target.value)} aria-label={t('form.month_aria')}>
            {Array.from({ length: 12 }, (_, i) => i + 1).map((m) => (
              <option key={m} value={m}>
                {new Date(2000, m - 1, 1).toLocaleDateString(intlLocale(i18n.language), { month: 'long' })}
              </option>
            ))}
          </select>
        )}

        {kind === 'bag_funding' && (
          <>
            {fundingAccounts.length === 0 ? (
              <p className="hint-text">{t('form.funding_no_options')}</p>
            ) : (
              <>
                <select
                  value={fundAccountId}
                  onChange={(e) => { setFundAccountId(e.target.value); setBagId(''); }}
                  aria-label={t('form.funding_account_aria')}
                  required
                >
                  {/* Only accounts that can legally receive a funding: an
                      account without open bags has no bag to target — the
                      option would be a no-op schedule. */}
                  <option value="">{t('form.funding_account_empty')}</option>
                  {fundingAccounts.map((a) => (
                    <option key={a.id} value={a.id}>{a.name}</option>
                  ))}
                </select>
                {fundAccountId && (
                  <select value={bagId} onChange={(e) => setBagId(e.target.value)} aria-label={t('form.bag_fund_aria')} required>
                    <option value="">{t('form.bag_fund_empty')}</option>
                    {fundAccountBags.map((b) => (
                      <option key={b.id} value={b.id}>{b.name}</option>
                    ))}
                  </select>
                )}
              </>
            )}
            {bagId && (
              <p className="hint-text">
                {t('form.bag_fund_hint', { label: bagAccountLabel(bagId, openBags) })}
              </p>
            )}
          </>
        )}
        {kind === 'bag_rebalance' && (
          <>
            <select value={fromBagId} onChange={(e) => { setFromBagId(e.target.value); setToBagId(''); }} aria-label={t('form.from_bag_aria')} required>
              <option value="">{t('form.from_bag_empty')}</option>
              {openBags.map((b) => (
                <option key={b.id} value={b.id}>{b.name} · {b.account_name}</option>
              ))}
            </select>
            <select value={toBagId} onChange={(e) => setToBagId(e.target.value)} aria-label={t('form.to_bag_aria')} required disabled={!fromBag}>
              <option value="">{t('form.to_bag_empty')}</option>
              {sameAccountBags.map((b) => (
                <option key={b.id} value={b.id}>{b.name} · {b.account_name}</option>
              ))}
            </select>
            {fromBagId && toBagId && (
              <p className="hint-text">
                {t('form.bag_rebalance_hint', { label: bagAccountLabel(fromBagId, openBags) })}
              </p>
            )}
          </>
        )}
        {(kind === 'account_transfer' || kind === 'transaction') && (
          <select
            value={accountId}
            onChange={(e) => { setAccountId(e.target.value); setTxBagId(''); }}
            aria-label={t('form.account_aria')}
            required
          >
            <option value="">{kind === 'account_transfer' ? t('form.from_account_empty') : t('form.on_account_empty')}</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        )}
        {kind === 'account_transfer' && (
          <select value={counterId} onChange={(e) => setCounterId(e.target.value)} aria-label={t('form.counter_aria')} required>
            <option value="">{t('form.to_account_empty')}</option>
            {accounts.filter((a) => String(a.id) !== accountId).map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        )}
        {kind === 'transaction' && (
          <select value={txKind} onChange={(e) => { setTxKind(e.target.value as 'expense' | 'income'); setTxBagId(''); }} aria-label={t('form.tx_kind_aria')}>
            <option value="expense">{t('form.tx_expense')}</option>
            <option value="income">{t('form.tx_income')}</option>
          </select>
        )}
        {kind === 'transaction' && txKind === 'expense' && accountBags.length > 0 && (
          <select value={txBagId} onChange={(e) => setTxBagId(e.target.value)} aria-label={t('form.tx_bag_aria')}>
            <option value="">{t('form.tx_bag_optional')}</option>
            {accountBags.map((b) => (
              <option key={b.id} value={b.id}>{b.name}</option>
            ))}
          </select>
        )}
        {kind === 'transaction' && (
          <select value={categoryId} onChange={(e) => setCategoryId(e.target.value)} aria-label={t('form.category_aria')}>
            <option value="">{t('form.category_none')}</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>{c.name}</option>
            ))}
          </select>
        )}
        {kind !== 'purchase' && (
          <MoneyInput
            valueCents={amountCents}
            currency={amountCurrency()}
            ariaLabel={t('form.amount_placeholder')}
            placeholder={t('form.amount_placeholder')}
            onCommit={setAmountCents}
          />
        )}
        {kind === 'transaction' && (
          <input
            placeholder={t('form.description_placeholder')}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={200}
          />
        )}
        {kind === 'purchase' && (
          <>
            <MoneyInput
              valueCents={estimateCents}
              currency={baseCurrency}
              ariaLabel={t('form.estimate_placeholder')}
              placeholder={t('form.estimate_placeholder')}
              onCommit={setEstimateCents}
            />
            <input
              placeholder={t('form.note_placeholder')}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              maxLength={200}
            />
          </>
        )}

        <p className="hint-text">
          {kind === 'bag_funding' || kind === 'bag_rebalance'
            ? t('form.hint_auto')
            : kind === 'purchase'
              ? t('form.hint_purchase')
              : t('form.hint_confirm_first')}
          {' '}{t('form.hint_editing_future')}
        </p>
        {validation && <ErrorMessage message={validation} />}
        {error && <ErrorMessage message={error} />}
        <div className="dialog-actions">
          <Button variant="ghost" onClick={onCancel} disabled={busy}>
            {t('form.cancel')}
          </Button>
          <Button variant="primary" type="submit" disabled={busy}>
            {initial ? t('form.save') : t('form.create')}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

// --- occurrences card (payables register) --------------------------------------

/** The unified occurrences register (MoneyWiz-style, paged ONE month at a
 *  time): the viewed month's occurrences with the owed/paid totals and the
 *  actions on the same row — apply (early payment allowed: the movement
 *  records at today's date, the occurrence keeps its due date), dismiss,
 *  skip, and edit/delete of the generating plan. Pending badges show the due
 *  countdown; applied/dismissed/skipped rows show their state. Virtual kinds
 *  apply themselves on the due date and carry no Apply button. The arrows
 *  page through months — a far month materializes on demand, so its pending
 *  occurrences are actionable the moment they are visible. */
function PayablesCard({
  month,
  items,
  totals,
  today,
  busy,
  describe,
  historyRows,
  showHistory,
  onToggleHistory,
  onAdd,
  onApply,
  onRunAction,
  onEdit,
  onArchive,
  onDelete,
  onShift,
}: {
  month: string; // YYYY-MM, the rendered month
  items: Payable[];
  totals: PayablesTotal[];
  today: string;
  busy: boolean;
  describe: (p: SchedulePayload) => string;
  historyRows: ScheduleRunView[];
  showHistory: boolean;
  onToggleHistory: () => void;
  onAdd: () => void;
  onApply: (it: Payable) => void;
  onRunAction: (it: Payable, action: 'dismiss' | 'skip') => void;
  onEdit: (it: Payable) => void;
  onArchive: (it: Payable) => void;
  onDelete: (it: Payable) => void;
  onShift: (delta: number) => void;
}) {
  const { t } = useTranslation('schedules');

  function monthLabel(m: string): string {
    return new Date(`${m}-01T00:00:00`).toLocaleDateString(intlLocale(i18n.language), {
      year: 'numeric',
      month: 'long',
    });
  }

  /** Pending rows get the due countdown ("overdue X" / "today" / "in N"),
   *  everything else its state badge. Red: overdue OR due within the next
   *  2 days (user rule 2026-10-28) — the row's dot reddens with the badge. */
  function badge(it: Payable): { label: string; danger: boolean } {
    if (it.status === 'pending') {
      const due: DueLabel = dueLabel(it.due_date, today);
      return {
        label: t(due.key, { count: due.count }),
        danger: it.due_date < today || daysBetween(today, it.due_date) <= 2,
      };
    }
    switch (it.status) {
      case 'applied':
        return { label: t('payables.status_paid'), danger: false };
      case 'dismissed':
        return { label: t(STATUS_KEYS.dismissed), danger: false };
      case 'skipped':
        return { label: t(STATUS_KEYS.skipped), danger: false };
      default:
        return { label: t('payables.status_projected'), danger: false };
    }
  }

  return (
    <Card title={t('payables.card')}>
      <div className="form-row">
        <Button variant="primary" onClick={onAdd}>
          {t('todo.add')}
        </Button>
      </div>
      <p className="hint-text">{t('payables.hint')}</p>
      <div className="payables-month-head">
        <span className="payables-month-nav">
          <IconButton icon={<IconChevronLeft />} label={t('payables.prev_month')} onClick={() => onShift(-1)} />
          <strong>{monthLabel(month)}</strong>
          <IconButton icon={<IconChevronRight />} label={t('payables.next_month')} onClick={() => onShift(1)} />
        </span>
        {totals.map((tt) => (
          <span key={tt.currency} className="cell-muted">
            {tt.currency}{' '}
            {t('payables.month_totals', {
              owed: formatCents(tt.owed_cents, tt.currency),
              paid: formatCents(tt.paid_cents, tt.currency),
            })}
          </span>
        ))}
      </div>
      <div className="budget-list">
        {items.map((it) => (
          <div key={`${it.action_id}-${it.due_date}`} className={badge(it).danger ? 'budget-card due-soon' : 'budget-card'}>
            <div className="budget-head">
              <span className={badge(it).danger ? 'budget-dot danger' : 'budget-dot'} aria-hidden="true" />
              <span className="budget-name">{it.name}</span>
              <span className={badge(it).danger ? 'badge danger' : 'badge'}>
                {badge(it).label}
              </span>
              <span className="budget-amounts">
                {it.amount_cents > 0 ? formatCents(it.amount_cents, it.currency) : '—'}
              </span>
            </div>
            <div className="budget-status">
              {t(KIND_KEYS[it.kind])} · {t(RECURRENCE_KEYS[it.recurrence])}
              {it.recurrence !== 'once' && it.day_of_month
                ? ` · ${t('list.day', { count: it.day_of_month })}${it.recurrence === 'yearly' && it.month_of_year ? ` ${t('list.month_of', { count: it.month_of_year })}` : ''}`
                : ''} · {formatDate(it.due_date)} · {describe(it.schedule)}
              {it.auto_apply && ` · ${t('payables.auto_marker')}`}
            </div>
            <div className="budget-actions">
              {it.status === 'pending' && it.run_id !== undefined && (
                VIRTUAL_KINDS.includes(it.kind) ? (
                  <span className="stat-label">{t('todo.applies_itself')}</span>
                ) : (
                  <>
                    <Button variant="primary" className="btn-sm" disabled={busy} onClick={() => onApply(it)}>
                      {it.kind === 'purchase' ? t('todo.done') : t('todo.apply')}
                    </Button>
                    <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => onRunAction(it, 'dismiss')}>
                      {t('todo.dismiss')}
                    </Button>
                    <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => onRunAction(it, 'skip')}>
                      {t('todo.skip')}
                    </Button>
                  </>
                )
              )}
              <span className="cell-actions">
                <IconButton icon={<IconEdit />} label={t('list.edit')} onClick={() => onEdit(it)} disabled={busy} />
                <IconButton icon={<IconArchive />} label={t('list.archive')} onClick={() => onArchive(it)} disabled={busy} />
                <IconButton icon={<IconTrash />} label={t('list.delete')} variant="danger" onClick={() => onDelete(it)} disabled={busy} />
              </span>
            </div>
          </div>
        ))}
        {items.length === 0 && <EmptyState message={t('payables.empty_month')} />}
      </div>
      {historyRows.length > 0 && (
        <div>
          <Button variant="ghost" onClick={onToggleHistory}>
            {showHistory ? t('list.hide_history') : t('list.show_history', { count: historyRows.length })}
          </Button>
          {showHistory && (
            <div className="budget-list">
              {[...historyRows]
                .sort((a, b) => b.due_date.localeCompare(a.due_date) || a.id - b.id)
                .map((run) => (
                <div key={run.id} className="budget-card">
                  <div className="budget-head">
                    <span className="budget-dot closed" aria-hidden="true" />
                    <span className="budget-name">{run.action_name}</span>
                    <span className="badge">{t(STATUS_KEYS[run.status])}</span>
                    <span className="budget-amounts">{formatDate(run.due_date)}</span>
                  </div>
                  <div className="budget-status">
                    {t(KIND_KEYS[run.kind])} · {describe(run.schedule)}
                    {run.result_type === 'transaction' && t('list.result_transaction')}
                    {run.result_type === 'allocation' && t('list.result_allocation')}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

/** Calendar-safe YYYY-MM shift (no timezone Date pitfalls). */
function shiftMonth(m: string, delta: number): string {
  const y = Number(m.slice(0, 4));
  const mo = Number(m.slice(5, 7)) + delta;
  const ny = y + Math.floor((mo - 1) / 12);
  const nm = ((mo - 1 + 12) % 12) + 1;
  return `${ny}-${String(nm).padStart(2, '0')}`;
}