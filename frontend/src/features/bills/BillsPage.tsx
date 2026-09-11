import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { useAsync } from '../../hooks/useAsync';
import { billsApi, type BillStatsGroupBy } from '../../api/bills';
import { accountsApi } from '../../api/accounts';
import { budgetsApi } from '../../api/budgets';
import { categoriesApi } from '../../api/categories';
import { storesApi } from '../../api/stores';
import type { Bill, BillDraft } from '../../types/domain';
import { formatCents, currentMonth } from '../../lib/money';
import { Card, Spinner, ErrorMessage, EmptyState, Button, ItemPanels, Dialog } from '../../components/ui';
import { BillDraftEditor, buildConfirmInput } from './BillDraftEditor';
import { BillDraftView } from './BillDraftView';

export function BillsPage() {
  const [month, setMonth] = useState(currentMonth());
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [groupBy, setGroupBy] = useState<BillStatsGroupBy>('market');
  const [statsMonth, setStatsMonth] = useState(currentMonth());
  const categories = useAsync(() => categoriesApi.list(), []);
  const brands = useAsync(() => billsApi.brands(), []);
  const stores = useAsync(() => storesApi.list(), []);
  const accounts = useAsync(() => accountsApi.list(), []);

  // Scans waiting for AI analysis or review — polled while any is analyzing.
  const scans = useAsync(() => billsApi.listScans(), []);
  const [scanBusyToken, setScanBusyToken] = useState<string | null>(null);
  const [scanError, setScanError] = useState<string | null>(null);
  // Dialog: a re-read was enqueued; the result arrives in this list later.
  const [rereadSent, setRereadSent] = useState(false);
  const hasAnalyzing = (scans.data ?? []).some((s) => s.status === 'analyzing');

  useEffect(() => {
    if (!hasAnalyzing) return;
    const timer = setInterval(scans.reload, 5000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasAnalyzing]);

  async function retryScan(token: string) {
    setScanBusyToken(token);
    setScanError(null);
    try {
      await billsApi.reextract(token);
      setRereadSent(true);
      scans.reload();
    } catch (err) {
      setScanError(err instanceof Error ? err.message : 'Retry failed.');
      scans.reload(); // a consumed token disappears from the list
    } finally {
      setScanBusyToken(null);
    }
  }

  async function discardScan(token: string) {
    setScanBusyToken(token);
    setScanError(null);
    try {
      await billsApi.discardScan(token);
    } catch (err) {
      setScanError(err instanceof Error ? err.message : 'Failed to discard the scan.');
    } finally {
      setScanBusyToken(null);
      scans.reload();
    }
  }

  const filters: { month?: string } = {};
  if (month) filters.month = month;

  const bills = useAsync(() => billsApi.list(filters), [month]);
  const stats = useAsync(() => billsApi.stats(groupBy, statsMonth || undefined), [
    groupBy,
    statsMonth,
  ]);

  // The list endpoint omits items; the full bill (with lines) is fetched
  // when a panel is expanded.
  const [detail, setDetail] = useState<Bill | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  // View ↔ edit toggle inside the expanded panel.
  const [editing, setEditing] = useState(false);
  const [editDraft, setEditDraft] = useState<BillDraft | null>(null);
  const [saving, setSaving] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  // Delete flow: the dialog asks for confirmation before the destructive call.
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const detailMonth = detail?.date ? detail.date.slice(0, 7) : '';
  const budgets = useAsync(
    () => (detailMonth ? budgetsApi.listByMonth(detailMonth) : Promise.resolve([])),
    [detailMonth],
  );

  async function loadDetail(id: number) {
    setDetail(null);
    setDetailError(null);
    setEditing(false);
    setEditDraft(null);
    setDetailLoading(true);
    try {
      setDetail(await billsApi.get(id));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : 'Failed to load bill details.');
    } finally {
      setDetailLoading(false);
    }
  }

  async function saveEdit(accountId?: number) {
    if (!detail || !editDraft) return;
    setSaving(true);
    setEditError(null);
    try {
      const updated = await billsApi.update(detail.id, buildConfirmInput(editDraft, accountId));
      setDetail(updated);
      setEditing(false);
      setEditDraft(null);
      bills.reload();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : 'Failed to save the bill.');
    } finally {
      setSaving(false);
    }
  }

  async function confirmDeleteBill() {
    if (confirmDeleteId === null) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await billsApi.remove(confirmDeleteId);
      setConfirmDeleteId(null);
      setExpandedId(null);
      setDetail(null);
      bills.reload();
      stats.reload();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete the bill.');
    } finally {
      setDeleting(false);
    }
  }

  const list: Bill[] = bills.data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">Bills &amp; analysis</h2>

      <Card title="Spending analysis">
        <div className="filter-row">
          <select
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value as BillStatsGroupBy)}
            aria-label="Group analysis by"
          >
            <option value="market">By market</option>
            <option value="month">By month</option>
            <option value="week">By week</option>
            <option value="item">By article</option>
            <option value="category">By category</option>
          </select>
          <input
            type="month"
            value={statsMonth}
            onChange={(e) => setStatsMonth(e.target.value)}
            aria-label="Limit analysis to month"
          />
        </div>
        {stats.loading ? (
          <Spinner />
        ) : stats.error ? (
          <ErrorMessage message={stats.error.message} />
        ) : (stats.data?.rows ?? []).length === 0 ? (
          <EmptyState message="No accepted bills yet — confirm a scanned bill to see analysis." />
        ) : (
          <div className="table-scroll">
            <table className="data-table">
              <thead>
                <tr>
                  <th>{statsLabel(groupBy)}</th>
                  {groupBy === 'item' && <th className="num">Qty</th>}
                  <th className="num">Bills</th>
                  <th className="num">Total ({stats.data?.currency})</th>
                </tr>
              </thead>
              <tbody>
                {(stats.data?.rows ?? []).map((row) => (
                  <tr key={row.label}>
                    <td>{row.label}</td>
                    {groupBy === 'item' && (
                      <td className="num">{row.quantity ?? '—'}</td>
                    )}
                    <td className="num">{row.bill_count}</td>
                    <td className="num">{formatCents(row.total_cents, stats.data?.currency ?? 'USD')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {(scans.data ?? []).length > 0 && (
        <Card title="Receipt analysis in progress">
          {scanError && <ErrorMessage message={scanError} />}
          <ItemPanels>
            {(scans.data ?? []).map((scan) => (
              <details key={scan.scan_token} className="item-panel">
                <summary>
                  <span className="item-icon">🧾</span>
                  <span className="item-title">
                    <span className="item-name">Receipt scan</span>
                    <span className="item-brand">
                      {scan.created_at
                        ? new Date(scan.created_at).toLocaleString()
                        : 'just now'}
                    </span>
                  </span>
                  <span
                    className={`badge ${
                      scan.status === 'analyzing'
                        ? 'badge-analyzing'
                        : scan.status === 'failed'
                          ? 'badge-failed'
                          : 'badge-draft'
                    }`}
                  >
                    {scan.status === 'analyzing'
                      ? 'Analyzing…'
                      : scan.status === 'failed'
                        ? 'Failed'
                        : 'Ready to review'}
                  </span>
                </summary>
                <div className="item-detail">
                  {scan.status === 'analyzing' && (
                    <>
                      <Spinner label="Reading the receipt…" />
                      <p className="hint-text">
                        Analysis runs in the background and can take a few
                        minutes — you can leave this page and check back later;
                        the draft appears here once it's ready.
                      </p>
                    </>
                  )}
                  {scan.status === 'failed' && (
                    <>
                      <ErrorMessage message={scan.error || 'Analysis failed.'} />
                      <div className="camera-row">
                        <Button
                          variant="secondary"
                          disabled={scanBusyToken !== null}
                          onClick={() => retryScan(scan.scan_token)}
                        >
                          🔁 Try again
                        </Button>
                        <Button
                          variant="secondary"
                          disabled={scanBusyToken !== null}
                          onClick={() => discardScan(scan.scan_token)}
                        >
                          Discard
                        </Button>
                      </div>
                    </>
                  )}
                  {scan.status === 'done' && (
                    <div className="camera-row">
                      <Link
                        className="btn btn-secondary"
                        to={`/scan?token=${scan.scan_token}`}
                      >
                        Review draft
                      </Link>
                    </div>
                  )}
                </div>
              </details>
            ))}
          </ItemPanels>
        </Card>
      )}

      <div className="filter-row">
        <input
          type="month"
          value={month}
          onChange={(e) => setMonth(e.target.value)}
          aria-label="Filter bills by month"
        />
      </div>

      {list.length === 0 ? (
        <EmptyState message="No bills match these filters." />
      ) : (
        <ItemPanels>
          {list.map((bill) => (
            <details
              key={bill.id}
              className="item-panel"
              open={expandedId === bill.id}
              onToggle={(e) => {
                const open = (e.currentTarget as HTMLDetailsElement).open;
                if (open && expandedId !== bill.id) {
                  setExpandedId(bill.id);
                  loadDetail(bill.id);
                } else if (!open && expandedId === bill.id) {
                  setExpandedId(null);
                  setDetail(null);
                  setDetailError(null);
                  setConfirmDeleteId(null);
                  setDeleteError(null);
                }
              }}
            >
              <summary>
                <span className="item-icon">🧾</span>
                <span className="item-title">
                  <span className="item-name">{bill.market_name || 'Unknown market'}</span>
                  <span className="item-brand">{bill.date || 'no date'}</span>
                </span>
                <span className="item-price">{formatCents(bill.total_cents, bill.currency)}</span>
              </summary>
              <div className="item-detail">
                {bill.id === expandedId &&
                  (detailLoading ? (
                    <Spinner />
                  ) : detailError ? (
                    <ErrorMessage message={detailError} />
                  ) : detail ? (
                    editing && editDraft ? (
                      <BillDraftEditor
                        mode="saved"
                        draft={editDraft}
                        onChange={setEditDraft}
                        onConfirm={saveEdit}
                        busy={saving ? 'confirm' : null}
                        error={editError}
                        accounts={(accounts.data ?? []).map((a) => ({
                          id: a.id,
                          name: a.name,
                          type: a.type,
                          card_last_digits: a.card_last_digits,
                        }))}
                        initialAccountId={detail.account_id ?? null}
                        categories={categories.data ?? []}
                        brands={brands.data ?? []}
                        budgets={budgets.data ?? []}
                        stores={stores.data ?? []}
                      />
                    ) : (
                      <>
                        <BillDraftView bill={detail} categories={categories.data ?? []} />
                        <div className="camera-row">
                          <Button
                            variant="secondary"
                            onClick={() => {
                              setEditDraft(billToDraft(detail));
                              setEditError(null);
                              setEditing(true);
                            }}
                          >
                            ✏️ Edit bill
                          </Button>
                          <Button
                            variant="danger"
                            onClick={() => {
                              setDeleteError(null);
                              setConfirmDeleteId(bill.id);
                            }}
                          >
                            🗑️ Delete bill
                          </Button>
                        </div>
                      </>
                    )
                  ) : null)}
              </div>
            </details>
          ))}
        </ItemPanels>
      )}

      {rereadSent && (
        <Dialog title="Re-read request sent">
          <p className="hint-text">
            The receipt has been queued for analysis in the background — this
            can take a few minutes. The result appears in this list once it's
            ready.
          </p>
          <div className="dialog-actions">
            <Button onClick={() => setRereadSent(false)}>OK</Button>
          </div>
        </Dialog>
      )}

      {confirmDeleteId !== null && detail && detail.id === confirmDeleteId && (
        <Dialog title="Delete bill?">
          <p className="hint-text">
            This permanently removes “{detail.market_name || 'Unknown market'}” (
            {formatCents(detail.total_cents, detail.currency)}) — its articles,
            the recorded expense transaction, and the stored receipt image. This
            cannot be undone.
          </p>
          {deleteError && <ErrorMessage message={deleteError} />}
          <div className="dialog-actions">
            <Button variant="secondary" disabled={deleting} onClick={() => setConfirmDeleteId(null)}>
              Cancel
            </Button>
            <Button variant="danger" disabled={deleting} onClick={confirmDeleteBill}>
              {deleting ? 'Deleting…' : '🗑️ Delete'}
            </Button>
          </div>
        </Dialog>
      )}
    </div>
  );
}

/** Converts a saved bill into the editor's draft shape. */
function billToDraft(bill: Bill): BillDraft {
  return {
    market_name: bill.market_name,
    date: bill.date,
    payment_method: bill.payment_method,
    card_last_digits: bill.card_last_digits || undefined,
    currency: bill.currency,
    items: (bill.items ?? []).map((it, i) => ({
      id: it.id || i + 1,
      name: it.name,
      brand: it.brand || undefined,
      unit: it.unit || undefined,
      category_name: it.category_name || undefined,
      category_id: it.category_id,
      quantity: it.quantity,
      unit_price_cents: it.unit_price_cents,
      discount_cents: it.discount_cents,
      line_total_cents: it.line_total_cents,
      is_return: it.is_return,
      budget_id: it.budget_id ?? undefined,
    })),
    items_subtotal_cents: bill.items_subtotal_cents,
    discount_cents: bill.discount_cents,
    vat_cents: bill.vat_cents,
    total_cents: bill.total_cents,
    printed_total_cents: bill.printed_total_cents,
    budget_id: bill.budget_id ?? undefined,
  };
}

function statsLabel(groupBy: BillStatsGroupBy): string {
  switch (groupBy) {
    case 'market':
      return 'Market';
    case 'month':
      return 'Month';
    case 'week':
      return 'Week';
    case 'item':
      return 'Article';
    case 'category':
      return 'Category';
  }
}