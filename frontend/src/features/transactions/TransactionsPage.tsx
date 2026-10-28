import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { useAsync } from '../../hooks/useAsync';
import { transactionsApi, type TransactionFilters } from '../../api/transactions';
import { billsApi } from '../../api/bills';
import { bagsApi } from '../../api/bags';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import { storesApi } from '../../api/stores';
import type {
  AccountLedger,
  LedgerEntry,
  Transaction,
  TransactionInput,
  TransactionKind,
} from '../../types/domain';
import { formatCents, formatSignedCents, currentMonth } from '../../lib/money';
import {
  Button,
  Card,
  Tabs,
  Spinner,
  ErrorMessage,
  EmptyState,
  Dialog,
  ItemPanel,
  ItemPanels,
} from '../../components/ui';
import { TransactionForm } from './TransactionForm';

// The register page window: a full account thread paginates newest-first with
// the same per-row running fold regardless of the window.
const REGISTER_PAGE = 50;

// The page's two jobs are parallel: reading the month's activity and entering
// a new transaction. Tabs keep the list clean; the form lives one click away.
type ViewTab = 'activity' | 'add';

export function TransactionsPage() {
  const { t, i18n: i18nInstance } = useTranslation('transactions');
  const [searchParams, setSearchParams] = useSearchParams();
  // The register deep-link from the accounts page: ?account={id} pins the
  // activity tab to that account's full thread with per-row running
  // balances. View-only — every write stays the plain list's business.
  const registerId = Number(searchParams.get('account')) || null;
  const [month, setMonth] = useState(currentMonth());
  const [kind, setKind] = useState<TransactionKind | ''>('');
  const [bagFilter, setBagFilter] = useState('');
  const [tab, setTab] = useState<ViewTab>('activity');
  const [savedNotice, setSavedNotice] = useState(false);

  // The saved notice self-clears after a few seconds.
  useEffect(() => {
    if (!savedNotice) return;
    const timer = setTimeout(() => setSavedNotice(false), 5000);
    return () => clearTimeout(timer);
  }, [savedNotice]);

  const filters: TransactionFilters = { month, limit: 100 };
  if (kind) filters.kind = kind;
  if (bagFilter) filters.bag_id = Number(bagFilter);

  // The register view reads its own endpoint (all-time thread); the month
  // list stays quiet while it is pinned.
  const { data, loading, error, reload } = useAsync(
    () => (registerId ? Promise.resolve<Transaction[]>([]) : transactionsApi.list(filters)),
    [month, kind, bagFilter, registerId],
  );
  const accounts = useAsync(() => accountsApi.list(), []);
  const categories = useAsync(() => categoriesApi.list(), []);
  const stores = useAsync(() => storesApi.list(), []);

  // The register thread: all-time, newest first, per-row running balances.
  // Pagination appends older pages; the per-row fold is window-independent
  // (the backend computes it over the whole ledger before slicing).
  const [register, setRegister] = useState<AccountLedger | null>(null);
  const [registerRows, setRegisterRows] = useState<LedgerEntry[]>([]);
  const [registerOffset, setRegisterOffset] = useState(0);
  const [registerNonce, setRegisterNonce] = useState(0); // forces a refetch when the ledger changed underneath
  const [registerDone, setRegisterDone] = useState(false);
  const [registerLoading, setRegisterLoading] = useState(false);
  const [registerError, setRegisterError] = useState<string | null>(null);

  useEffect(() => {
    if (!registerId) return;
    let cancelled = false;
    setRegisterLoading(true);
    accountsApi
      .ledger(registerId, REGISTER_PAGE, registerOffset)
      .then((res) => {
        if (cancelled) return;
        setRegister(res);
        setRegisterRows((prev) =>
          registerOffset === 0 ? res.entries : [...prev, ...res.entries],
        );
        setRegisterDone(res.entries.length < REGISTER_PAGE);
        setRegisterError(null);
      })
      .catch((err) => {
        if (cancelled) return;
        setRegisterError(err instanceof Error ? err.message : t('errors.load'));
      })
      .finally(() => {
        if (cancelled) return;
        setRegisterLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [registerId, registerOffset, registerNonce]);

  // A deep-link switch (or link-out) resets the register window.
  useEffect(() => {
    setRegister(null);
    setRegisterRows([]);
    setRegisterOffset(0);
    setRegisterDone(false);
    setRegisterError(null);
  }, [registerId]);

  function reloadRegister() {
    setRegisterOffset(0);
    // The nonce makes the fetch effect refire even when the offset was
    // already 0.
    setRegisterNonce((n) => n + 1);
  }
  // Bag filter options: every bag that has its own activity view.
  const bagsView = useAsync(() => bagsApi.overview(), []);

  // Expanded panel: the list endpoint omits item lines, so expanding a
  // transaction with items fetches the full record (like the bills view).
  const [createFormKey, setCreateFormKey] = useState(0);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [detail, setDetail] = useState<Transaction | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  // Edit modal (manual transactions only — bill transactions are readonly).
  const [editing, setEditing] = useState<Transaction | null>(null);
  const [editLoading, setEditLoading] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<Transaction | null>(null);
  const [pageError, setPageError] = useState<string | null>(null);

  /** Display label for a stored kind value (the segments and detail field share it). */
  function kindLabel(value: TransactionKind): string {
    switch (value) {
      case 'expense':
        return t('kind.displayExpense');
      case 'income':
        return t('kind.displayIncome');
      case 'transfer':
        return t('kind.displayTransfer');
      default:
        return value;
    }
  }

  async function loadDetail(id: number) {
    setDetail(null);
    setDetailError(null);
    setDetailLoading(true);
    try {
      setDetail(await transactionsApi.get(id));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : t('errors.loadDetails'));
    } finally {
      setDetailLoading(false);
    }
  }

  /** Opens the edit modal with the full record (list rows omit item lines). */
  async function openEdit(tx: Transaction) {
    setPageError(null);
    setEditLoading(true);
    setEditOpen(true);
    try {
      setEditing(tx.item_count ? await transactionsApi.get(tx.id) : tx);
    } catch (err) {
      setEditOpen(false);
      setPageError(err instanceof Error ? err.message : t('errors.load'));
    } finally {
      setEditLoading(false);
    }
  }

  async function handleEditSave(input: TransactionInput) {
    if (!editing) return;
    const updated = await transactionsApi.update(editing.id, input);
    setEditOpen(false);
    setEditing(null);
    reload();
    // An edit rewrites a row's change and every later running balance —
    // the register re-renders from a fresh page 0.
    if (registerId) reloadRegister();
    if (detail?.id === updated.id) setDetail(updated);
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setDeleting(true);
    setPageError(null);
    try {
      if (confirmDelete.bill_id != null) {
        // A bill-recorded transaction is deleted THROUGH its bill: the bill
        // cascade removes the item lines, the receipt file and this very
        // transaction — there is no "delete only the mirror" operation.
        await billsApi.remove(confirmDelete.bill_id);
      } else {
        await transactionsApi.remove(confirmDelete.id);
      }
      setConfirmDelete(null);
      if (expandedId === confirmDelete.id) {
        setExpandedId(null);
        setDetail(null);
      }
      reload();
      if (registerId) reloadRegister();
    } catch (err) {
      setPageError(err instanceof Error ? err.message : t('errors.delete'));
    } finally {
      setDeleting(false);
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
  for (const tx of transactions) {
    const last = dayGroups[dayGroups.length - 1];
    // Transfers are neither income nor spend: they move money between the
    // user's own accounts, so the day total stays neutral to them.
    const signed = tx.kind === 'income' ? tx.amount_cents : tx.kind === 'transfer' ? 0 : -tx.amount_cents;
    if (last && last.date === tx.date) {
      last.total += signed;
      last.items.push(tx);
    } else {
      dayGroups.push({
        date: tx.date,
        label: dayLabel(tx.date, intlLocale(i18nInstance.language)),
        total: signed,
        currency: tx.currency,
        items: [tx],
      });
    }
  }

  // Register mode groups the thread by day too, but the day total is the
  // account's exact cash flow: every ledger row carries its change (the fold
  // term — foreign-currency rows already folded, transfers their fee/counter
  // leg), so transfers count instead of staying neutral. The running balance
  // travels beside the row for the subtitle.
  const accountCurrency = register?.account.currency ?? '';
  const ledgerById = new Map<number, LedgerEntry>(registerRows.map((r) => [r.id, r]));
  const registerGroups = dayGroups; // reuse the grouping for the ledger rows
  if (registerId) {
    registerGroups.length = 0;
    for (const row of registerRows) {
      const last = registerGroups[registerGroups.length - 1];
      if (last && last.date === row.date) {
        last.total += row.change_cents;
        last.items.push(row);
      } else {
        registerGroups.push({
          date: row.date,
          label: dayLabel(row.date, intlLocale(i18nInstance.language)),
          total: row.change_cents,
          currency: accountCurrency,
          items: [row],
        });
      }
    }
  }

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <Tabs
        ariaLabel={t('tabs.viewsAria')}
        tabs={[
          { key: 'activity', label: t('tabs.activity') },
          { key: 'add', label: t('tabs.add') },
        ]}
        active={tab}
        onChange={setTab}
      />

      {savedNotice && tab === 'activity' && (
        <div className="hint-banner">{t('notice.saved')}</div>
      )}

      {tab === 'activity' && (
        <>
          {registerId ? (
            <div className="hint-banner">
              {registerLoading && registerRows.length === 0 ? (
                <Spinner label={t('register.loading')} />
              ) : (
                <span>
                  <strong>{register?.account.name ?? `#${registerId}`}</strong>
                  {register && (
                    <>
                      {' · '}
                      {t('register.balance')}
                      <strong>{formatCents(register.account.balance_cents, register.account.currency)}</strong>
                    </>
                  )}
                </span>
              )}
              <Link to="/transactions" onClick={() => setSearchParams({})}>
                {t('register.showAll')}
              </Link>
            </div>
          ) : (
            <>
              <div className="filter-row">
                <input
                  type="month"
                  value={month}
                  onChange={(e) => setMonth(e.target.value)}
                  aria-label={t('filters.monthAria')}
                />
                {(bagsView.data?.accounts ?? []).some((v) => v.bags.length > 0) && (
                  <select
                    value={bagFilter}
                    onChange={(e) => setBagFilter(e.target.value)}
                    aria-label={t('filters.bagAria')}
                  >
                    <option value="">{t('filters.allBags')}</option>
                    {(bagsView.data?.accounts ?? []).flatMap((v) =>
                      v.bags.map((b) => (
                        <option key={b.id} value={b.id}>
                          {b.name} ({v.account_name})
                        </option>
                      )),
                    )}
                  </select>
                )}
                <Button
                  variant="primary"
                  className="btn-sm"
                  onClick={() => setTab('add')}
                >
                  {t('action.add')}
                </Button>
              </div>
              <div className="segmented" role="group" aria-label={t('filters.kindAria')}>
                {(
                  [
                    ['', t('kind.all')],
                    ['expense', t('kind.expense')],
                    ['income', t('kind.income')],
                    ['transfer', t('kind.transfer')],
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
            </>
          )}
          {registerId && registerError && <ErrorMessage message={registerError} />}
        </>
      )}

      {tab === 'add' && (
        <Card title={t('card.newTransaction')}>
          <TransactionForm
            // Remount on success so the next entry starts empty.
            key={createFormKey}
            mode="create"
            accounts={accounts.data ?? []}
            categories={categories.data ?? []}
            stores={stores.data ?? []}
            onSubmit={async (input) => {
              await transactionsApi.create(input);
              setCreateFormKey((k) => k + 1);
              setSavedNotice(true);
              setTab('activity');
              reload();
            }}
          />
        </Card>
      )}

      {pageError && <ErrorMessage message={pageError} />}

      {(registerId ? registerRows.length === 0 && !registerLoading : transactions.length === 0) ? (
        <EmptyState
          message={registerId ? t('empty.register') : t('empty.month', { month })}
        />
      ) : (
        <div className="day-group">
          {(registerId ? registerGroups : dayGroups).map((group) => (
            <section key={group.date} aria-label={t('day.sectionAria', { label: group.label, total: formatCents(group.total, group.currency) })}>
              <div className="day-header">
                <span>{group.label}</span>
                <span>{formatSignedCents(group.total, group.currency)}</span>
              </div>
              <ItemPanels>
                {group.items.map((tx) => {
                  const isBill = tx.bill_id != null;
                  const isIncome = tx.kind === 'income';
                  const isTransfer = tx.kind === 'transfer';
                  const cat = tx.category_id ? categoryNames.get(tx.category_id) : undefined;
                  const account = accountNames.get(tx.account_id);
                  const counterAccount = tx.counter_account_id ? accountNames.get(tx.counter_account_id) : undefined;
                  const items = tx.id === expandedId ? (detail?.items ?? null) : null;
                  // Register mode: the row's own fold (change) and the balance
                  // as it stood right after this row.
                  const row = registerId ? ledgerById.get(tx.id) : undefined;
                  return (
                    <ItemPanel
                      key={tx.id}
                      title={tx.description || t('items.none')}
                      subtitle={
                        [
                          isTransfer
                            ? [
                                t('subtitle.transfer'),
                                `${account ?? `#${tx.account_id}`} → ${counterAccount ?? `#${tx.counter_account_id}`}`,
                              ].join(' · ')
                            : [
                                isBill ? t('subtitle.bill') : tx.store_name || null,
                                cat ?? t('subtitle.uncategorized'),
                                account,
                                tx.bag_name || null,
                              ]
                                .filter(Boolean)
                                .join(' · '),
                          row
                            ? t('register.balanceAfter', {
                                balance: formatSignedCents(row.running_balance_cents, accountCurrency),
                              })
                            : null,
                        ]
                          .filter(Boolean)
                          .join(' · ')
                      }
                      value={
                        row
                          ? formatSignedCents(row.change_cents, accountCurrency)
                          : formatCents(tx.amount_cents, tx.currency)
                      }
                      valueClass={
                        row ? (row.change_cents > 0 ? 'stat-positive' : '') : isIncome ? 'stat-positive' : ''
                      }
                      onToggle={(open) => {
                        if (open && expandedId !== tx.id) {
                          setExpandedId(tx.id);
                          if (tx.item_count && tx.item_count > 0) loadDetail(tx.id);
                        } else if (!open && expandedId === tx.id) {
                          setExpandedId(null);
                          setDetail(null);
                          setDetailError(null);
                        }
                      }}
                    >
                      {isBill ? (
                        // Recorded for a scanned bill: readonly for edits
                        // here, but deleting goes through the bill — the
                        // confirm dialog spells out that the bill, its item
                        // lines and the stored receipt go with it.
                        <>
                          <div className="item-field-grid">
                            <div className="item-field">
                              <span>{t('field.date')}</span>
                              <span>{tx.date}</span>
                            </div>
                            <div className="item-field">
                              <span>{t('field.amount')}</span>
                              <strong>{formatCents(tx.amount_cents, tx.currency)}</strong>
                            </div>
                          </div>
                          <p className="hint-text">
                            {t('bill.hint')}
                          </p>
                          <div className="camera-row">
                            <Link className="btn btn-secondary" to={`/bills?bill=${tx.bill_id}`}>
                              {t('bill.view')}
                            </Link>
                            <Button
                              variant="danger"
                              className="btn-sm"
                              onClick={() => {
                                setPageError(null);
                                setConfirmDelete(tx);
                              }}
                            >
                              {t('action.delete')}
                            </Button>
                          </div>
                        </>
                      ) : (
                        <>
                          <div className="item-field-grid">
                            <div className="item-field">
                              <span>{t('field.date')}</span>
                              <span>{tx.date}</span>
                            </div>
                            <div className="item-field">
                              <span>{t('field.kind')}</span>
                              <span>{kindLabel(tx.kind)}</span>
                            </div>
                            <div className="item-field">
                              <span>{t('field.amount')}</span>
                              <strong>{formatCents(tx.amount_cents, tx.currency)}</strong>
                            </div>
                          </div>
                          {isTransfer ? (
                            <div className="item-field-grid">
                              <div className="item-field">
                                <span>{t('field.from')}</span>
                                <span>{account ?? `#${tx.account_id}`}</span>
                              </div>
                              <div className="item-field">
                                <span>{t('field.to')}</span>
                                <span>{counterAccount ?? tx.counter_account_name ?? '#' + (tx.counter_account_id ?? '?')}</span>
                              </div>
                              <div className="item-field">
                                <span>{t('field.amount')}</span>
                                <span>{formatCents(tx.amount_cents, tx.currency)}</span>
                              </div>
                              {tx.fee_cents ? (
                                <div className="item-field">
                                  <span>{t('field.fee')}</span>
                                  <span>{formatCents(tx.fee_cents, tx.currency)}</span>
                                </div>
                              ) : null}
                              {tx.counter_amount_cents != null && tx.counter_currency ? (
                                <div className="item-field">
                                  <span>{t('field.received')}</span>
                                  <span>{formatCents(tx.counter_amount_cents, tx.counter_currency)}</span>
                                </div>
                              ) : null}
                            </div>
                          ) : (
                            <div className="item-field-grid">
                              <div className="item-field">
                                <span>{t('field.category')}</span>
                                <span>{cat ?? '—'}</span>
                              </div>
                              <div className="item-field">
                                <span>{t('field.account')}</span>
                                <span>{account ?? `#${tx.account_id}`}</span>
                              </div>
                              <div className="item-field">
                                <span>{t('field.store')}</span>
                                <span>{tx.store_name ?? '—'}</span>
                              </div>
                            </div>
                          )}

                          {tx.item_count != null && tx.item_count > 0 && (
                            <div className="tx-items">
                              <div className="transaction-lines-header">
                                <span>{t('items.count', { count: tx.item_count })}</span>
                                {detail?.items_total_cents != null && (
                                  <span>
                                    {t('items.total', { total: formatCents(detail.items_total_cents, tx.currency) })}
                                  </span>
                                )}
                              </div>
                              {tx.id === expandedId &&
                                (detailLoading ? (
                                  <Spinner label={t('items.loading')} />
                                ) : detailError ? (
                                  <ErrorMessage message={detailError} />
                                ) : items ? (
                                  <>
                                    {items.map((it) => (
                                      <div className="tx-item-row" key={it.id}>
                                        <span className="tx-item-name">
                                          {it.name}
                                          {it.brand ? <span className="tx-item-brand"> · {it.brand}</span> : null}
                                          {it.standard_name &&
                                            it.standard_name.toLowerCase() !== it.name.toLowerCase() && (
                                              <span className="tx-item-brand"> ↳ {it.standard_name}</span>
                                            )}
                                          {it.generic_name && (
                                            <span className="tx-item-brand"> · {it.generic_name}</span>
                                          )}
                                          {it.product_id == null && (
                                            <span className="tx-item-brand"> {t('items.notLinked')}</span>
                                          )}
                                        </span>
                                        <span className="tx-item-meta">
                                          {it.quantity} × {formatCents(it.unit_price_cents, tx.currency)}
                                          {it.discount_cents > 0
                                            ? ` − ${formatCents(it.discount_cents, tx.currency)}`
                                            : ''}
                                        </span>
                                        <strong className="tx-item-total">
                                          {formatCents(it.line_total_cents, tx.currency)}
                                        </strong>
                                      </div>
                                    ))}
                                    {detail?.items_total_cents !== undefined &&
                                      detail?.items_total_cents !== tx.amount_cents && (
                                        <p className="hint-text">
                                          {t('items.diffHint')}
                                        </p>
                                      )}
                                  </>
                                ) : null)}
                            </div>
                          )}

                          <div className="camera-row">
                            <Button variant="secondary" className="btn-sm" onClick={() => openEdit(tx)}>
                              {t('action.edit')}
                            </Button>
                            <Button
                              variant="danger"
                              className="btn-sm"
                              onClick={() => {
                                setPageError(null);
                                setConfirmDelete(tx);
                              }}
                            >
                              {t('action.delete')}
                            </Button>
                          </div>
                        </>
                      )}
                    </ItemPanel>
                  );
                })}
              </ItemPanels>
            </section>
          ))}
        </div>
      )}

      {registerId && !registerDone && !registerError && (
        <div className="camera-row">
          <Button
            variant="secondary"
            className="btn-sm"
            disabled={registerLoading}
            onClick={() => setRegisterOffset((o) => o + REGISTER_PAGE)}
          >
            {t('register.showMore')}
          </Button>
        </div>
      )}

      {editOpen && (
        <Dialog title={t('dialog.editTitle')} wide>
          {editLoading || !editing ? (
            <Spinner label={t('dialog.loading')} />
          ) : (
            <TransactionForm
              mode="edit"
              initial={editing}
              accounts={accounts.data ?? []}
              categories={categories.data ?? []}
              stores={stores.data ?? []}
              onSubmit={handleEditSave}
              onCancel={() => {
                setEditOpen(false);
                setEditing(null);
              }}
            />
          )}
        </Dialog>
      )}

      {confirmDelete && (
        <Dialog title={t('dialog.deleteTitle')}>
          <p className="hint-text">
            {confirmDelete.bill_id != null
              ? t('dialog.deleteBillBody', {
                  name: confirmDelete.description || t('dialog.defaultName'),
                  amount: formatCents(confirmDelete.amount_cents, confirmDelete.currency),
                })
              : t('dialog.deleteBody', {
                  name: confirmDelete.description || t('dialog.defaultName'),
                  amount: formatCents(confirmDelete.amount_cents, confirmDelete.currency),
                })}
          </p>
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={deleting}>
              {t('action.cancel')}
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={deleting}>
              {deleting ? t('action.deleting') : t('action.delete')}
            </Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}

/** Section header for a transaction day: "Today", "Yesterday", or
 *  "SAT, SEP 12" for older dates. */
function dayLabel(iso: string, locale: string): string {
  const [y, m, d] = iso.split('-').map(Number);
  const day = new Date(y ?? 2000, (m ?? 1) - 1, d ?? 1);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const daysAgo = Math.round((today.getTime() - day.getTime()) / 86_400_000);
  if (daysAgo === 0) return i18n.t('transactions:time.today');
  if (daysAgo === 1) return i18n.t('transactions:time.yesterday');
  return day
    .toLocaleDateString(locale, { weekday: 'short', month: 'short', day: 'numeric' })
    .toUpperCase();
}