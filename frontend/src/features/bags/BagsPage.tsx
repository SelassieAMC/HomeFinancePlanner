import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { bagsApi } from '../../api/bags';
import { accountsApi } from '../../api/accounts';
import { dollarsToCents, formatCents, formatSignedCents } from '../../lib/money';
import { todayISO } from '../../lib/date';
import type {
  BagStatus,
  BagInput,
  BagAllocation,
  BagAllocationInput,
  BagRebalanceInput,
  YearClosePreview,
  YearCloseBagRow,
} from '../../types/domain';
import {
  Button,
  Card,
  Dialog,
  ErrorMessage,
  EmptyState,
  Spinner,
  IconButton,
  IconCloseCircle,
  IconEdit,
  IconFund,
  IconLedger,
  IconMoveBetween,
  IconReopen,
  IconSliders,
  IconTrash,
  IconWithdraw,
  ProgressBar,
} from '../../components/ui';

// Bags are named reserves INSIDE one account ("allocation envelopes"): the
// bank application is untouched, the bag's balance is derived per read as
// Σ allocations − tagged spend, and the account section shows the money still
// unbagged. Same card language as budgets (the closest cousin in the UI).
export function BagsPage({ embedded = false }: { embedded?: boolean }) {
  const { t } = useTranslation('bags');
  const overview = useAsync(() => bagsApi.overview(), []);
  const accounts = useAsync(() => accountsApi.list(), []);

  const [formError, setFormError] = useState<string | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [createName, setCreateName] = useState('');
  const [createAmount, setCreateAmount] = useState('');
  const [createAccount, setCreateAccount] = useState('');
  const [createTarget, setCreateTarget] = useState('');

  const [editing, setEditing] = useState<BagStatus | null>(null);
  const [allocating, setAllocating] = useState<BagStatus | null>(null);
  // The allocate dialog opens with a specific action when launched from the
  // per-kind buttons (fund / withdraw); the "Move money" button leaves the
  // choice open.
  const [allocateKind, setAllocateKind] = useState<'fund' | 'withdraw' | 'adjust'>('fund');
  const [historyOf, setHistoryOf] = useState<{ bag: BagStatus; currency: string; rows: BagAllocation[] } | null>(null);
  const [rebalancing, setRebalancing] = useState<string>(''); // account id as string
  // Moves launched from a bag card preselect the source bag.
  const [rebalanceFrom, setRebalanceFrom] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<BagStatus | null>(null);
  const [yearClose, setYearClose] = useState<YearClosePreview | null>(null);

  async function refresh() {
    try {
      await overview.reload();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.reload'));
    }
  }

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    if (!createName.trim()) {
      setFormError(t('validation.nameRequired'));
      return;
    }
    if (!createAccount) {
      setFormError(t('validation.accountRequired'));
      return;
    }
    const input: BagInput = {
      name: createName,
      account_id: Number(createAccount),
      target_cents: null,
    };
    if (createAmount) {
      const cents = dollarsToCents(createAmount);
      if (!Number.isFinite(cents) || cents <= 0) {
        setFormError(t('validation.targetPositive'));
        return;
      }
      input.target_cents = cents;
    }
    try {
      await bagsApi.create(input);
      setCreateName('');
      setCreateAmount('');
      setCreateTarget('');
      await refresh();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.create'));
    }
  }

  async function handleAllocate(bag: BagStatus, input: BagAllocationInput) {
    await bagsApi.allocate(bag.id, input);
    await refresh();
  }

  async function handleRebalance(input: BagRebalanceInput) {
    await bagsApi.rebalance(input);
    await refresh();
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setBusy(true);
    setPageError(null);
    try {
      await bagsApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      await refresh();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.delete'));
    } finally {
      setBusy(false);
    }
  }

  async function handleStatus(bag: BagStatus, status: 'open' | 'closed') {
    setBusy(true);
    setPageError(null);
    try {
      await bagsApi.setStatus(bag.id, status);
      await refresh();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.change'));
    } finally {
      setBusy(false);
    }
  }

  async function openHistory(bag: BagStatus) {
    const currency = (overview.data?.accounts ?? []).find((a) => a.account_id === bag.account_id)?.currency ?? '';
    setHistoryOf({ bag, currency, rows: [] });
    try {
      const rows = await bagsApi.allocations(bag.id);
      setHistoryOf({ bag, currency, rows });
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.ledger'));
      setHistoryOf(null);
    }
  }

  async function openYearClose(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const year = new FormData(e.currentTarget).get('year')?.toString() ?? '';
    setPageError(null);
    setBusy(true);
    try {
      setYearClose(await bagsApi.yearClose(year));
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.closePreview'));
    } finally {
      setBusy(false);
    }
  }

  if (overview.loading) return <Spinner />;
  if (overview.error) return <ErrorMessage message={overview.error.message} />;

  const accountsData = accounts.data ?? [];
  const sections = overview.data?.accounts ?? [];

  const content = (
    <>
      <Card title={t('add.title')}>
        <form className="form-row" onSubmit={handleCreate}>
          <input
            placeholder={t('add.namePlaceholder')}
            value={createName}
            onChange={(e) => setCreateName(e.target.value)}
            maxLength={100}
            aria-label={t('add.nameAria')}
            required
          />
          <select
            value={createAccount}
            onChange={(e) => setCreateAccount(e.target.value)}
            aria-label={t('add.accountAria')}
            required
          >
            <option value="">{t('add.accountPlaceholder')}</option>
            {accountsData.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.currency})
              </option>
            ))}
          </select>
          <input
            placeholder={t('add.targetPlaceholder')}
            inputMode="decimal"
            value={createTarget}
            onChange={(e) => setCreateTarget(e.target.value)}
            aria-label={t('add.targetAria')}
          />
          <Button type="submit">{t('actions.add')}</Button>
        </form>
        <p className="hint-text">
          {t('add.hint')}
        </p>
        {formError && <ErrorMessage message={formError} />}
      </Card>

      {pageError && <ErrorMessage message={pageError} />}

      <div className="form-row">
        <form className="form-row" onSubmit={openYearClose}>
          <input type="number" name="year" defaultValue={new Date().getFullYear()} min={2000} max={2999} aria-label={t('dialogs.yearClose.yearAria')} />
          <Button type="submit" variant="secondary" disabled={busy}>
            {t('actions.yearPreview')}
          </Button>
        </form>
      </div>

      {sections.length === 0 ? (
        <EmptyState message={t('list.empty')} />
      ) : (
        sections.map((view) => (
          <section key={view.account_id} className="bag-account-section">
            <div className="bag-account-head">
              <h3 className="bag-account-title">
                {view.account_name}
                <span className="stat-label"> · {formatCents(view.account_balance_cents, view.currency)}</span>
              </h3>
              <div className="bag-unbagged">
                <span className="stat-label">{t('account.unbagged')}</span>
                <strong className={view.oversubscribed ? 'budget-status over' : ''}>
                  {formatCents(view.unbagged_cents, view.currency)}
                </strong>
                {view.oversubscribed && (
                  <span className="budget-status over">{t('account.oversubscribed')}</span>
                )}
              </div>
              {view.bags.some((b) => b.status === 'open') && (
                <Button variant="secondary" className="btn-sm" onClick={() => setRebalancing(String(view.account_id))}>
                  {t('actions.rebalance')}
                </Button>
              )}
            </div>
            {view.bags.length === 0 ? (
              <p className="hint-text">{t('list.noBagsOnAccount')}</p>
            ) : (
              <div className="table-scroll">
                <table className="plan-table">
                  <thead>
                    <tr>
                      <th>{t('table.bag')}</th>
                      <th className="num">{t('table.target')}</th>
                      <th className="num">{t('table.balance')}</th>
                      <th className="num">{t('table.spent')}</th>
                      <th className="cell-progress-header">{t('table.progress')}</th>
                      <th>{t('table.status')}</th>
                      <th aria-hidden="true" />
                    </tr>
                  </thead>
                  <tbody>
                    {view.bags.map((b) => {
                      const health = bagHealth(b);
                      const pct =
                        b.target_cents != null && b.target_cents > 0
                          ? (b.balance_cents / b.target_cents) * 100
                          : 0;
                      const openSiblings =
                        view.bags.filter((o) => o.status === 'open').length > 1;
                      return (
                        <tr key={b.id}>
                          <td>
                            <span className="cell-entity">
                              <span
                                className={
                                  health === 'ok'
                                    ? 'budget-dot ok'
                                    : health === 'over'
                                      ? 'budget-dot over'
                                      : 'budget-dot'
                                }
                                aria-hidden="true"
                              />
                              <span className="entity-name">{b.name}</span>
                              {b.status === 'closed' && <BagClosedTag label={t('status.closed')} />}
                            </span>
                          </td>
                          <td className="num">
                            {b.target_cents != null ? formatCents(b.target_cents, view.currency) : '—'}
                          </td>
                          <td className="num">
                            <strong>{formatCents(b.balance_cents, view.currency)}</strong>
                          </td>
                          <td className="num cell-muted-num">
                            {formatCents(b.tagged_spend_cents, view.currency)}
                          </td>
                          <td className="cell-progress">
                            {b.target_cents != null && b.target_cents > 0 ? (
                              <ProgressBar pct={pct} done={health === 'ok'} over={health === 'over'} />
                            ) : (
                              ''
                            )}
                          </td>
                          <td>
                            <BagStatusCell bag={b} currency={view.currency} />
                          </td>
                          <td className="cell-actions">
                            {b.status === 'open' && (
                              <>
                                <IconButton
                                  icon={<IconFund />}
                                  label={t('actions.fund')}
                                  disabled={busy}
                                  onClick={() => {
                                    setAllocateKind('fund');
                                    setAllocating(b);
                                  }}
                                />
                                <IconButton
                                  icon={<IconWithdraw />}
                                  label={t('actions.withdraw')}
                                  disabled={busy}
                                  onClick={() => {
                                    setAllocateKind('withdraw');
                                    setAllocating(b);
                                  }}
                                />
                                {openSiblings && (
                                  <IconButton
                                    icon={<IconMoveBetween />}
                                    label={t('actions.moveToBag')}
                                    disabled={busy}
                                    onClick={() => {
                                      setRebalanceFrom(b.id);
                                      setRebalancing(String(view.account_id));
                                    }}
                                  />
                                )}
                                <IconButton
                                  icon={<IconSliders />}
                                  label={t('actions.adjust')}
                                  disabled={busy}
                                  onClick={() => {
                                    setAllocateKind('fund');
                                    setAllocating(b);
                                  }}
                                />
                              </>
                            )}
                            <IconButton
                              icon={<IconLedger />}
                              label={t('actions.ledger')}
                              disabled={busy}
                              onClick={() => void openHistory(b)}
                            />
                            <IconButton
                              icon={<IconEdit />}
                              label={t('actions.edit')}
                              disabled={busy}
                              onClick={() => setEditing(b)}
                            />
                            <IconButton
                              icon={b.status === 'open' ? <IconCloseCircle /> : <IconReopen />}
                              label={b.status === 'open' ? t('actions.closeBag') : t('actions.reopen')}
                              disabled={busy}
                              onClick={() =>
                                void handleStatus(b, b.status === 'open' ? 'closed' : 'open')
                              }
                            />
                            <IconButton
                              icon={<IconTrash />}
                              label={t('actions.delete')}
                              variant="danger"
                              disabled={busy}
                              onClick={() => setConfirmDelete(b)}
                            />
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        ))
      )}

      {editing && (
        <Dialog title={t('dialogs.edit.title')}>
          <BagEditForm
            initial={editing}
            currency={sections.find((a) => a.account_id === editing.account_id)?.currency ?? ''}
            busy={busy}
            onSubmit={async (input) => {
              await bagsApi.update(editing.id, input);
              setEditing(null);
              await refresh();
            }}
            onCancel={() => setEditing(null)}
            onError={setPageError}
          />
        </Dialog>
      )}

      {allocating && (
        <Dialog title={t('dialogs.allocate.title', { name: allocating.name })}>
          <AllocateForm
            currency={sections.find((a) => a.account_id === allocating.account_id)?.currency ?? ''}
            initialKind={allocateKind}
            busy={busy}
            onSubmit={async (input) => {
              await handleAllocate(allocating, input);
              setAllocating(null);
            }}
            onCancel={() => setAllocating(null)}
          />
        </Dialog>
      )}

      {historyOf && (
        <Dialog title={t('dialogs.history.title', { name: historyOf.bag.name })}>
          <LedgerList rows={historyOf.rows} currency={historyOf.currency} />
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setHistoryOf(null)}>
              {t('actions.close')}
            </Button>
          </div>
        </Dialog>
      )}

      {rebalancing && (
        <Dialog title={t('dialogs.rebalance.title')}>
          <RebalanceForm
            account={sections.find((a) => String(a.account_id) === rebalancing)}
            initialFromBag={rebalanceFrom}
            busy={busy}
            onSubmit={async (input) => {
              await handleRebalance(input);
              setRebalancing('');
            }}
            onCancel={() => {
              setRebalancing('');
              setRebalanceFrom(null);
            }}
          />
        </Dialog>
      )}

      {yearClose && (
        <Dialog title={t('dialogs.yearClose.title', { year: yearClose.year })}>
          <YearCloseList
            preview={yearClose}
            onSubmit={async () => {
              setYearClose(null);
              await refresh();
            }}
            onError={setPageError}
            busy={busy}
          />
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={t('dialogs.delete.title')}>
          <p className="hint-text">
            {t('dialogs.delete.body', { name: confirmDelete.name })}
          </p>
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={busy}>
              {t('actions.cancel')}
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={busy}>
              {busy ? t('actions.deleting') : t('actions.delete')}
            </Button>
          </div>
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

function bagHealth(b: BagStatus): string | undefined {
  if (b.status === 'closed') return 'closed';
  if (b.balance_cents < 0) return 'over';
  if (b.target_cents != null && b.balance_cents < b.target_cents) return 'low';
  if (b.balance_cents > 0) return 'ok';
  return 'spent-out';
}

// The BagCard progress-card became one table row per bag under its account
// section: aligned figure cells, a bar cell, and icon actions — Fund and
// Withdraw post the everyday ledger entries, "Move to bag" preselects the
// rebalance source, Adjust (a rare drift correction) opens the kind selector.
/** Closed-bag inline tag beside the bag name. */
function BagClosedTag({ label }: { label: string }) {
  return <span className="cell-tag stat-label">{label}</span>;
}

/** The derived health wording of a bag row, colored by state. */
function BagStatusCell({ bag, currency }: { bag: BagStatus; currency: string }) {
  const { t } = useTranslation('bags');
  return (
    <span
      className={
bag.balance_cents < 0
          ? 'cell-status over'
          : 'cell-status'
      }
    >
      <span
        className={
          bag.balance_cents < 0
            ? 'budget-dot over'
            : bag.status === 'closed'
              ? 'budget-dot closed'
              : 'budget-dot'
        }
        aria-hidden="true"
      />
      {bag.status === 'closed'
        ? t('status.closed')
        : bag.balance_cents < 0
          ? t('status.overspent', { amount: formatCents(-bag.balance_cents, currency) })
          : bag.balance_cents === 0
            ? t('status.spentOut')
            : t('status.holdsReserve')}
    </span>
  );
}

function BagEditForm({
  initial,
  currency,
  busy,
  onSubmit,
  onCancel,
  onError,
}: {
  initial: BagStatus;
  currency: string;
  busy: boolean;
  onSubmit: (input: BagInput) => Promise<void>;
  onCancel: () => void;
  onError: (msg: string | null) => void;
}) {
  const { t } = useTranslation('bags');
  const [name, setName] = useState(initial.name);
  const [target, setTarget] = useState(
    initial.target_cents != null ? (initial.target_cents / 100).toString() : '',
  );
  const [formError, setFormError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    onError(null);
    const input: BagInput = { name, account_id: initial.account_id, target_cents: null };
    if (target) {
      const cents = dollarsToCents(target);
      if (!Number.isFinite(cents) || cents <= 0) {
        setFormError(t('validation.targetPositive'));
        return;
      }
      input.target_cents = cents;
    }
    try {
      await onSubmit(input);
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.save'));
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        {t('dialogs.edit.nameLabel')}
        <input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} required />
      </label>
      <label>
        {t('dialogs.edit.targetLabel', { currency })}
        <input
          inputMode="decimal"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        />
      </label>
      <p className="hint-text">
        {t('dialogs.edit.notice')}
      </p>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          {t('actions.cancel')}
        </Button>
        <Button type="submit" disabled={busy}>
          {busy ? t('actions.saving') : t('actions.saveChanges')}
        </Button>
      </div>
    </form>
  );
}

function AllocateForm({
  currency,
  initialKind = 'fund',
  busy,
  onSubmit,
  onCancel,
}: {
  currency: string;
  initialKind?: 'fund' | 'withdraw' | 'adjust';
  busy: boolean;
  onSubmit: (input: BagAllocationInput) => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation('bags');
  const [kind, setKind] = useState<'fund' | 'withdraw' | 'adjust'>(initialKind);
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [date, setDate] = useState(todayISO());
  const [formError, setFormError] = useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    const cents = dollarsToCents(amount);
    if (kind === 'adjust') {
      if (!Number.isFinite(cents) || cents === 0) {
        setFormError(t('validation.adjustNonZero'));
        return;
      }
    } else if (!Number.isFinite(cents) || cents <= 0) {
      setFormError(t('validation.positiveKind'));
      return;
    }
    try {
      await onSubmit({ kind, amount_cents: cents, note, date });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.allocate'));
    }
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <label>
        {t('dialogs.allocate.actionLabel')}
        <select value={kind} onChange={(e) => setKind(e.target.value as typeof kind)}>
          <option value="fund">{t('dialogs.allocate.kindFund')}</option>
          <option value="withdraw">{t('dialogs.allocate.kindWithdraw')}</option>
          <option value="adjust">{t('dialogs.allocate.kindAdjust')}</option>
        </select>
      </label>
      <label>
        {t('dialogs.allocate.amountLabel', { currency })}
        <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} required />
      </label>
      <label>
        {t('dialogs.allocate.noteLabel')}
        <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} />
      </label>
      <label>
        {t('dialogs.allocate.dateLabel')}
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
      </label>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          {t('actions.cancel')}
        </Button>
        <Button type="submit" disabled={busy}>
          {t('actions.post')}
        </Button>
      </div>
    </form>
  );
}

function LedgerList({ rows, currency }: { rows: BagAllocation[]; currency: string }) {
  const { t } = useTranslation('bags');
  if (rows.length === 0) {
    return <EmptyState message={t('ledger.empty')} />;
  }
  return (
    <div className="ledger-list">
      {rows.map((a) => (
        <div key={a.id} className="ledger-row">
          <span className="ledger-kind">{t(`ledger.kinds.${a.kind}`)}</span>
          <span className="ledger-amounts">{formatSignedCents(a.amount_cents, currency)}</span>
          <span className="ledger-meta">
            {a.date}
            {a.note ? ` · ${a.note}` : ''}
          </span>
        </div>
      ))}
    </div>
  );
}

function RebalanceForm({
  account,
  initialFromBag = null,
  busy,
  onSubmit,
  onCancel,
}: {
  account:
    | {
        account_id: number;
        currency: string;
        bags: BagStatus[];
      }
    | undefined;
  initialFromBag?: number | null;
  busy: boolean;
  onSubmit: (input: BagRebalanceInput) => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation('bags');
  const open = (account?.bags ?? []).filter((b) => b.status === 'open');
  const [fromBag, setFromBag] = useState(initialFromBag ? String(initialFromBag) : '');
  const [toBag, setToBag] = useState('');
  const [amount, setAmount] = useState('');
  const [note, setNote] = useState('');
  const [date, setDate] = useState(todayISO());
  const [formError, setFormError] = useState<string | null>(null);
  const from = open.find((b) => String(b.id) === fromBag);
  const others = open.filter((b) => String(b.id) !== fromBag);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    if (!fromBag || !toBag) {
      setFormError(t('validation.bothBags'));
      return;
    }
    const cents = dollarsToCents(amount);
    if (!Number.isFinite(cents) || cents <= 0) {
      setFormError(t('validation.positive'));
      return;
    }
    try {
      await onSubmit({
        from_bag_id: Number(fromBag),
        to_bag_id: Number(toBag),
        amount_cents: cents,
        note,
        date,
      });
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.rebalance'));
    }
  }

  if (open.length < 2) {
    return (
      <>
        <EmptyState message={t('dialogs.rebalance.needTwo')} />
        <div className="dialog-actions">
          <Button variant="secondary" onClick={onCancel}>
            {t('actions.close')}
          </Button>
        </div>
      </>
    );
  }

  return (
    <form className="form-stack" onSubmit={handleSubmit}>
      <div className="form-row">
        <select value={fromBag} onChange={(e) => setFromBag(e.target.value)} aria-label={t('dialogs.rebalance.fromAria')} required>
          <option value="">{t('dialogs.rebalance.fromPlaceholder')}</option>
          {open.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name} ({formatCents(b.balance_cents, account?.currency ?? '')})
            </option>
          ))}
        </select>
        <select value={toBag} onChange={(e) => setToBag(e.target.value)} aria-label={t('dialogs.rebalance.toAria')} required>
          <option value="">{t('dialogs.rebalance.toPlaceholder')}</option>
          {others.map((b) => (
            <option key={b.id} value={b.id}>
              {b.name}
            </option>
          ))}
        </select>
      </div>
      <label>
        {t('dialogs.allocate.amountLabel', { currency: account?.currency ?? '' })}
        <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} required />
      </label>
      <label>
        {t('dialogs.allocate.noteLabel')}
        <input value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} />
      </label>
      <label>
        {t('dialogs.allocate.dateLabel')}
        <input type="date" value={date} onChange={(e) => setDate(e.target.value)} required />
      </label>
      {from && from.balance_cents < 0 && (
        <p className="hint-text">{t('dialogs.rebalance.warnOverspending')}</p>
      )}
      <p className="hint-text">{t('dialogs.rebalance.crossAccountHint')}</p>
      {formError && <ErrorMessage message={formError} />}
      <div className="form-actions">
        <Button type="button" variant="secondary" onClick={onCancel} disabled={busy}>
          {t('actions.cancel')}
        </Button>
        <Button type="submit" disabled={busy}>
          {t('actions.move')}
        </Button>
      </div>
    </form>
  );
}

// YearCloseList turns the preview into actions: a leftover bag can move its
// remaining balance into another bag of the same account (a rebalance dated
// at the year end) or release it. The close-out posts through the regular
// actions — no bespoke state.
function YearCloseList({
  preview,
  busy,
  onSubmit,
  onError,
}: {
  preview: YearClosePreview;
  busy: boolean;
  onSubmit: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const { t } = useTranslation('bags');
  const anyRows = preview.bags.length > 0;
  return (
    <div className="year-close">
      {anyRows && (
        <p className="hint-text">
          {t('dialogs.yearClose.hint', { year: preview.year })}
        </p>
      )}
      {!anyRows && <EmptyState message={t('dialogs.yearClose.empty')} />}
      <div className="budget-list">
        {preview.bags.map((row) => (
          <YearCloseRow
            key={row.bag_id}
            row={row}
            year={preview.year}
            bags={preview.bags.filter((b) => b.account_id === row.account_id && b.bag_id !== row.bag_id)}
            busy={busy}
            onDone={onSubmit}
            onError={onError}
          />
        ))}
      </div>
      {preview.conversion_warnings.length > 0 && (
        <ErrorMessage
          message={t('dialogs.yearClose.conversionWarning', { currencies: preview.conversion_warnings.join(', ') })}
        />
      )}
    </div>
  );
}

function YearCloseRow({
  row,
  year,
  bags,
  busy,
  onDone,
  onError,
}: {
  row: YearCloseBagRow;
  year: string;
  bags: YearCloseBagRow[];
  busy: boolean;
  onDone: () => Promise<void>;
  onError: (msg: string | null) => void;
}) {
  const { t } = useTranslation('bags');
  const closeDate = `${year}-12-31`;
  const [targetBag, setTargetBag] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  async function moveAll() {
    setFormError(null);
    onError(null);
    try {
      await bagsApi.rebalance({
        from_bag_id: row.bag_id,
        to_bag_id: Number(targetBag),
        amount_cents: row.balance_cents,
        note: t('dialogs.yearClose.notes.move', { year }),
        date: closeDate,
      });
      await onDone();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.move'));
    }
  }

  async function release() {
    setFormError(null);
    onError(null);
    try {
      await bagsApi.allocate(row.bag_id, {
        kind: 'withdraw',
        amount_cents: row.balance_cents,
        note: t('dialogs.yearClose.notes.release', { year }),
        date: closeDate,
      });
      await onDone();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : t('errors.release'));
    }
  }

  return (
    <Card className="budget-card">
      <div className="budget-head">
        <span className={row.shortfall_cents > 0 ? 'budget-dot' : 'budget-dot ok'} aria-hidden="true" />
        <span className="budget-name">{row.bag_name}</span>
        <span className="stat-label">{row.account_name}</span>
        <span className="budget-amounts">
          <strong>{formatCents(row.balance_cents, row.currency)}</strong>
          {row.target_cents > 0 && <span>/{formatCents(row.target_cents, row.currency)}</span>}
        </span>
      </div>
      <div className="budget-status">
        {row.shortfall_cents > 0
          ? t('dialogs.yearClose.row.shortOfTarget', { amount: formatCents(row.shortfall_cents, row.currency) })
          : row.target_cents > 0
            ? t('dialogs.yearClose.row.targetCovered')
            : row.balance_cents < 0
              ? t('status.overspent', { amount: formatCents(-row.balance_cents, row.currency) })
              : t('dialogs.yearClose.row.noTarget')}
      </div>
      {row.balance_cents > 0 && (
        <div className="camera-row">
          {bags.length > 0 && (
            <>
              <select value={targetBag} onChange={(e) => setTargetBag(e.target.value)} aria-label={t('dialogs.yearClose.row.moveToAria')}>
                <option value="">{t('dialogs.yearClose.row.moveToPlaceholder')}</option>
                {bags.map((b) => (
                  <option key={b.bag_id} value={b.bag_id}>
                    {b.bag_name}
                  </option>
                ))}
              </select>
              <Button
                variant="secondary"
                className="btn-sm"
                disabled={busy || !targetBag}
                onClick={() => void moveAll()}
              >
                {t('actions.moveAll')}
              </Button>
            </>
          )}
          <Button
            variant="ghost"
            className="btn-sm"
            onClick={() => void release()}
            disabled={busy}
          >
            {t('actions.releaseLeftover')}
          </Button>
        </div>
      )}
      {formError && <ErrorMessage message={formError} />}
    </Card>
  );
}