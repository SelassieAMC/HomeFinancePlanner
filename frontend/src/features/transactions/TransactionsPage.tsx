import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useAsync } from '../../hooks/useAsync';
import { transactionsApi, type TransactionFilters } from '../../api/transactions';
import { accountsApi } from '../../api/accounts';
import { categoriesApi } from '../../api/categories';
import { storesApi } from '../../api/stores';
import type { Transaction, TransactionInput, TransactionKind } from '../../types/domain';
import { formatCents, formatSignedCents, currentMonth } from '../../lib/money';
import { Button, Card, Spinner, ErrorMessage, EmptyState, Dialog, ItemPanel, ItemPanels } from '../../components/ui';
import { TransactionForm } from './TransactionForm';

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
  const stores = useAsync(() => storesApi.list(), []);

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

  async function loadDetail(id: number) {
    setDetail(null);
    setDetailError(null);
    setDetailLoading(true);
    try {
      setDetail(await transactionsApi.get(id));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : 'Failed to load transaction details.');
    } finally {
      setDetailLoading(false);
    }
  }

  /** Opens the edit modal with the full record (list rows omit item lines). */
  async function openEdit(t: Transaction) {
    setPageError(null);
    setEditLoading(true);
    setEditOpen(true);
    try {
      setEditing(t.item_count ? await transactionsApi.get(t.id) : t);
    } catch (err) {
      setEditOpen(false);
      setPageError(err instanceof Error ? err.message : 'Failed to load the transaction.');
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
    if (detail?.id === updated.id) setDetail(updated);
  }

  async function handleDelete() {
    if (!confirmDelete) return;
    setDeleting(true);
    setPageError(null);
    try {
      await transactionsApi.remove(confirmDelete.id);
      setConfirmDelete(null);
      if (expandedId === confirmDelete.id) {
        setExpandedId(null);
        setDetail(null);
      }
      reload();
    } catch (err) {
      // e.g. 409 when the bill-linked guard fired for a stale row.
      setPageError(err instanceof Error ? err.message : 'Failed to delete transaction.');
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
            reload();
          }}
        />
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

      {pageError && <ErrorMessage message={pageError} />}

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
                  const isBill = t.bill_id != null;
                  const isIncome = t.kind === 'income';
                  const cat = t.category_id ? categoryNames.get(t.category_id) : undefined;
                  const account = accountNames.get(t.account_id);
                  const items = t.id === expandedId ? (detail?.items ?? null) : null;
                  return (
                    <ItemPanel
                      key={t.id}
                      icon={isBill ? '🧾' : isIncome ? '💰' : '🛒'}
                      title={t.description || '(no description)'}
                      subtitle={[
                        isBill ? 'Bill' : t.store_name || null,
                        cat ?? 'Uncategorized',
                        account,
                      ]
                        .filter(Boolean)
                        .join(' · ')}
                      value={formatCents(t.amount_cents, t.currency)}
                      valueClass={isIncome ? 'stat-positive' : ''}
                      onToggle={(open) => {
                        if (open && expandedId !== t.id) {
                          setExpandedId(t.id);
                          if (t.item_count && t.item_count > 0) loadDetail(t.id);
                        } else if (!open && expandedId === t.id) {
                          setExpandedId(null);
                          setDetail(null);
                          setDetailError(null);
                        }
                      }}
                    >
                      {isBill ? (
                        // Recorded for a scanned bill: readonly here — the
                        // bill view is where it can be edited or deleted.
                        <>
                          <div className="item-field-grid">
                            <div className="item-field">
                              <span>Date</span>
                              <span>{t.date}</span>
                            </div>
                            <div className="item-field">
                              <span>Amount</span>
                              <strong>{formatCents(t.amount_cents, t.currency)}</strong>
                            </div>
                          </div>
                          <p className="hint-text">
                            This transaction was recorded for a scanned bill and
                            is managed there.
                          </p>
                          <div className="camera-row">
                            <Link className="btn btn-secondary" to={`/bills?bill=${t.bill_id}`}>
                              View bill
                            </Link>
                          </div>
                        </>
                      ) : (
                        <>
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
                            <div className="item-field">
                              <span>Store</span>
                              <span>{t.store_name ?? '—'}</span>
                            </div>
                          </div>

                          {t.item_count != null && t.item_count > 0 && (
                            <div className="tx-items">
                              <div className="transaction-lines-header">
                                <span>Items ({t.item_count})</span>
                                {detail?.items_total_cents != null && (
                                  <span>
                                    Items total {formatCents(detail.items_total_cents, t.currency)}
                                  </span>
                                )}
                              </div>
                              {t.id === expandedId &&
                                (detailLoading ? (
                                  <Spinner label="Loading items…" />
                                ) : detailError ? (
                                  <ErrorMessage message={detailError} />
                                ) : items ? (
                                  <>
                                    {items.map((it) => (
                                      <div className="tx-item-row" key={it.id}>
                                        <span className="tx-item-name">
                                          {it.name}
                                          {it.brand ? <span className="tx-item-brand"> · {it.brand}</span> : null}
                                          {it.product_id == null && (
                                            <span className="tx-item-brand"> (not linked)</span>
                                          )}
                                        </span>
                                        <span className="tx-item-meta">
                                          {it.quantity} × {formatCents(it.unit_price_cents, t.currency)}
                                          {it.discount_cents > 0
                                            ? ` − ${formatCents(it.discount_cents, t.currency)}`
                                            : ''}
                                        </span>
                                        <strong className="tx-item-total">
                                          {formatCents(it.line_total_cents, t.currency)}
                                        </strong>
                                      </div>
                                    ))}
                                    {detail?.items_total_cents !== undefined &&
                                      detail?.items_total_cents !== t.amount_cents && (
                                        <p className="hint-text">
                                          The amount differs from the items total — the
                                          difference stays in the transaction (delivery,
                                          fees or bags).
                                        </p>
                                      )}
                                  </>
                                ) : null)}
                            </div>
                          )}

                          <div className="camera-row">
                            <Button variant="secondary" onClick={() => openEdit(t)}>
                              ✏️ Edit
                            </Button>
                            <Button
                              variant="danger"
                              onClick={() => {
                                setPageError(null);
                                setConfirmDelete(t);
                              }}
                            >
                              🗑️ Delete
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

      {editOpen && (
        <Dialog title="Edit transaction" wide>
          {editLoading || !editing ? (
            <Spinner label="Loading…" />
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
        <Dialog title="Delete transaction?">
          <p className="hint-text">
            This permanently removes “{confirmDelete.description || 'this transaction'}” (
            {formatCents(confirmDelete.amount_cents, confirmDelete.currency)}) and its
            item lines. This cannot be undone.
          </p>
          <div className="dialog-actions">
            <Button variant="secondary" onClick={() => setConfirmDelete(null)} disabled={deleting}>
              Cancel
            </Button>
            <Button variant="danger" onClick={handleDelete} disabled={deleting}>
              {deleting ? 'Deleting…' : 'Delete'}
            </Button>
          </div>
        </Dialog>
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