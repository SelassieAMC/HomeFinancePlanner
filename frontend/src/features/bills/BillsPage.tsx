import { useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { billsApi } from '../../api/bills';
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
  const { t } = useTranslation('bills');
  const [month, setMonth] = useState(currentMonth());
  const [expandedId, setExpandedId] = useState<number | null>(null);
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
      setScanError(err instanceof Error ? err.message : t('errors.retry'));
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
      setScanError(err instanceof Error ? err.message : t('errors.discard'));
    } finally {
      setScanBusyToken(null);
      scans.reload();
    }
  }

  // Aborts an in-progress analysis (the AI read is stopped server-side); the
  // row turns "cancelled" — still deletable or re-readable from here.
  async function cancelScan(token: string) {
    setScanBusyToken(token);
    setScanError(null);
    try {
      await billsApi.cancelScan(token);
      scans.reload();
    } catch (err) {
      setScanError(err instanceof Error ? err.message : t('errors.cancel'));
      scans.reload();
    } finally {
      setScanBusyToken(null);
    }
  }

  const filters: { month?: string } = {};
  if (month) filters.month = month;

  const bills = useAsync(() => billsApi.list(filters), [month]);

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
  // Rescan (bill editor): re-run the AI read of the expanded bill's stored
  // receipt. Works like a fresh scan — the backend removes the bill and the
  // fresh draft appears in the receipt analyses for confirmation.
  const [rescanBusy, setRescanBusy] = useState(false);
  // Dialog: a bill rescan was started (the bill is gone; the fresh draft will
  // arrive in the receipt analyses).
  const [rescanSent, setRescanSent] = useState(false);
  // Delete flow: the dialog asks for confirmation before the destructive call.
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  // Budget envelopes are open-ended — any of them can be attached to a bill
  // regardless of the bill's date, so no month filtering here.
  const budgets = useAsync(() => budgetsApi.list('open'), []);

  /** Opens the edit view for the expanded bill (button at the top and bottom). */
  function startEdit(current: Bill) {
    setEditDraft(billToDraft(current));
    setEditError(null);
    setEditing(true);
  }

  async function loadDetail(id: number) {
    setDetail(null);
    setDetailError(null);
    setEditing(false);
    setEditDraft(null);
    setDetailLoading(true);
    try {
      setDetail(await billsApi.get(id));
    } catch (err) {
      setDetailError(err instanceof Error ? err.message : t('errors.detail'));
    } finally {
      setDetailLoading(false);
    }
  }

  // Deep link from the transactions view (?bill=<id>): jump to the bill's
  // month, expand it and load its lines. One-shot like the scan view's
  // ?token= resume; the query param is cleared afterwards.
  const [searchParams, setSearchParams] = useSearchParams();
  const deepLinkRef = useRef(false);
  useEffect(() => {
    if (deepLinkRef.current) return;
    const raw = searchParams.get('bill');
    if (!raw) return;
    deepLinkRef.current = true;
    const id = Number(raw);
    if (!Number.isInteger(id) || id <= 0) {
      setSearchParams({}, { replace: true });
      return;
    }
    billsApi
      .get(id)
      .then((bill) => {
        setMonth(bill.date.slice(0, 7));
        setExpandedId(bill.id);
        return loadDetail(bill.id);
      })
      .catch(() => {
        // Unknown id: silently drop the link, the list shows as usual.
      })
      .finally(() => setSearchParams({}, { replace: true }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function saveEdit(accountId?: number, _createCardAccount?: boolean, fxRate?: number) {
    if (!detail || !editDraft) return;
    setSaving(true);
    setEditError(null);
    try {
      const updated = await billsApi.update(
        detail.id,
        buildConfirmInput(
          editDraft,
          accountId,
          (accounts.data ?? []).map((a) => ({
            id: a.id,
            type: a.type,
            card_last_digits: a.card_last_digits,
          })),
          fxRate,
        ),
      );
      setDetail(updated);
      setEditing(false);
      setEditDraft(null);
      bills.reload();
    } catch (err) {
      setEditError(err instanceof Error ? err.message : t('errors.save'));
    } finally {
      setSaving(false);
    }
  }

  /**
   * Re-reads the expanded bill's receipt the way a fresh scan works: the
   * backend removes the bill (its recorded expense goes with it) and hands
   * its stored receipt to a normal scan pipeline row — background extraction,
   * and the fresh draft appears in the receipt analyses for confirmation like
   * any scanned bill. The edit view closes; the bill list re-loads without
   * it.
   */
  async function startRescan() {
    if (!detail || rescanBusy) return;
    setEditError(null);
    setRescanBusy(true);
    try {
      await billsApi.rescanBill(detail.id);
      // The bill is gone — leave the edit view and refresh the lists. The new
      // scan shows up in the receipt analyses (scans.reload()).
      setEditing(false);
      setEditDraft(null);
      setDetail(null);
      setExpandedId(null);
      bills.reload();
      scans.reload();
      setRescanSent(true);
    } catch (err) {
      setEditError(err instanceof Error ? err.message : t('errors.reread'));
    } finally {
      setRescanBusy(false);
    }
  }

  /** Leaves the edit view without saving. */
  function cancelEdit() {
    setEditDraft(null);
    setEditError(null);
    setEditing(false);
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
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : t('errors.delete'));
    } finally {
      setDeleting(false);
    }
  }

  const list: Bill[] = bills.data ?? [];

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      {/* The spending analysis lives in the dashboard's Products/Trends tabs
          (per-store, per-item, charts) under the dashboard's one period
          picker — a second month-scoped section here duplicated that with a
          second picker and was removed. */}

      {(scans.data ?? []).length > 0 && (
        <Card title={t('scanCard.title')}>
          {scanError && <ErrorMessage message={scanError} />}
          <ItemPanels>
            {(scans.data ?? []).map((scan) => (
              <details key={scan.scan_token} className="item-panel">
                <summary>
                  <span className="item-title">
                    <span className="item-name">
                      {t('scanCard.name')}
                      {(scan.file_count ?? 1) > 1 && ` — ${t('scanCard.multi', { n: scan.file_count ?? 1 })}`}
                    </span>
                    <span className="item-brand">
                      {scan.created_at
                        ? new Date(scan.created_at).toLocaleString()
                        : t('scanCard.justNow')}
                    </span>
                  </span>
                  <span
                    className={`badge ${
                      scan.status === 'analyzing'
                        ? 'badge-analyzing'
                        : scan.status === 'failed' || scan.status === 'cancelled'
                          ? 'badge-failed'
                          : 'badge-draft'
                    }`}
                  >
                    {scan.status === 'analyzing'
                      ? t('status.analyzing')
                      : scan.status === 'failed'
                        ? t('status.failed')
                        : scan.status === 'cancelled'
                          ? t('status.cancelled')
                          : t('status.ready')}
                  </span>
                </summary>
                <div className="item-detail">
                  {scan.status === 'analyzing' && (
                    <>
                      <Spinner label={t('scanCard.reading')} />
                      <p className="hint-text">
                        {t('scanCard.backgroundHint')}
                      </p>
                      <div className="camera-row">
                        <Button
                          variant="danger"
                          disabled={scanBusyToken !== null}
                          onClick={() => cancelScan(scan.scan_token)}
                        >
                          {t('action.cancelAnalysisIcon')}
                        </Button>
                      </div>
                    </>
                  )}
                  {(scan.status === 'failed' || scan.status === 'cancelled') && (
                    <>
                      {scan.status === 'failed' && (
                        <ErrorMessage message={scan.error || t('errors.analysisFailed')} />
                      )}
                      {scan.status === 'cancelled' && (
                        <p className="hint-text">{t('scanCard.cancelledHint')}</p>
                      )}
                      <div className="camera-row">
                        <Button
                          variant="secondary"
                          disabled={scanBusyToken !== null}
                          onClick={() => retryScan(scan.scan_token)}
                        >
                          {t('action.tryAgain')}
                        </Button>
                        <Button
                          variant="danger"
                          disabled={scanBusyToken !== null}
                          onClick={() => discardScan(scan.scan_token)}
                        >
                          {t('action.deleteRequest')}
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
                        {t('action.reviewDraft')}
                      </Link>
                      <Button
                        variant="danger"
                        disabled={scanBusyToken !== null}
                        onClick={() => discardScan(scan.scan_token)}
                      >
                        {t('action.deleteRequest')}
                      </Button>
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
          aria-label={t('filterMonthAria')}
        />
      </div>

      {list.length === 0 ? (
        <EmptyState message={t('listEmpty')} />
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
                <span className="item-title">
                  <span className="item-name">{bill.market_name || t('common.unknownMarket')}</span>
                  <span className="item-brand">{bill.date || t('common.noDate')}</span>
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
                        onRescan={startRescan}
                        rescanBusy={rescanBusy}
                        onCancel={cancelEdit}
                        error={editError}
                        accounts={(accounts.data ?? []).map((a) => ({
                          id: a.id,
                          name: a.name,
                          type: a.type,
                          currency: a.currency,
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
                        <div className="bill-actions">
                          <Button
                            variant="secondary"
                            onClick={() => startEdit(detail)}
                          >
                            {t('action.editBill')}
                          </Button>
                        </div>
                        <BillDraftView bill={detail} categories={categories.data ?? []} />
                        <div className="camera-row">
                          <Button
                            variant="secondary"
                            onClick={() => startEdit(detail)}
                          >
                            {t('action.editBill')}
                          </Button>
                          <Button
                            variant="danger"
                            onClick={() => {
                              setDeleteError(null);
                              setConfirmDeleteId(bill.id);
                            }}
                          >
                            {t('action.deleteBill')}
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
        <Dialog title={t('rereadDialog.title')}>
          <p className="hint-text">
            {t('rereadDialog.billsBody')}
          </p>
          <div className="dialog-actions">
            <Button onClick={() => setRereadSent(false)}>{t('common.ok')}</Button>
          </div>
        </Dialog>
      )}

      {rescanSent && (
        <Dialog title={t('rescanDialog.title')}>
          <p className="hint-text">
            {t('rescanDialog.body')}
          </p>
          <div className="dialog-actions">
            <Button onClick={() => setRescanSent(false)}>{t('common.ok')}</Button>
          </div>
        </Dialog>
      )}

      {confirmDeleteId !== null && detail && detail.id === confirmDeleteId && (
        <Dialog title={t('deleteDialog.title')}>
          <p className="hint-text">
            {t('deleteDialog.body', {
              market: detail.market_name || t('common.unknownMarket'),
              amount: formatCents(detail.total_cents, detail.currency),
            })}
          </p>
          {deleteError && <ErrorMessage message={deleteError} />}
          <div className="dialog-actions">
            <Button variant="secondary" disabled={deleting} onClick={() => setConfirmDeleteId(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="danger" disabled={deleting} onClick={confirmDeleteBill}>
              {deleting ? t('common.deleting') : t('common.delete')}
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
      standard_name: it.standard_name || undefined,
      generic_name: it.generic_name || undefined,
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
      bag_id: it.bag_id ?? undefined,
    })),
    items_subtotal_cents: bill.items_subtotal_cents,
    discount_cents: bill.discount_cents,
    vat_cents: bill.vat_cents,
    total_cents: bill.total_cents,
    printed_total_cents: bill.printed_total_cents,
    budget_id: bill.budget_id ?? undefined,
    bag_id: bill.bag_id ?? undefined,
  };
}