import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Dialog } from '../../components/ui';
import { ChartCard } from './charts/ChartCard';
import { rangeLabel } from '../../lib/date';
import { formatCents } from '../../lib/money';
import type { TopProducts } from '../../types/analytics';

/** How many rows a ranked consumption card shows inline; the rest live in the
 *  view-all modal. Must stay in sync with the backend defaultTopRows. */
export const TOP_LIST_INLINE = 10;

/** Shared modal for a ranked list's overflow: the dialog lists every row the
 *  API returned (the fetch goes out with the backend's ?limit= cap) and a
 *  Close action — the Dialog itself is visual only. */
export function TopListDialog({
  title,
  open,
  children,
  onClose,
}: {
  title: string;
  open: boolean;
  children: ReactNode;
  onClose: () => void;
}) {
  const { t } = useTranslation('dashboard');
  if (!open) return null;
  return (
    <Dialog title={title} wide onClose={onClose}>
      {children}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={onClose}>
          {t('lists.close')}
        </Button>
      </div>
    </Dialog>
  );
}

/** Step-3 consumption card: the period's most expensive products, ranked by
 *  converted spend with the bought quantity and the average price per piece.
 *  Shows the top 10 inline; "View all" opens the full ranked list in a modal.
 *  The total of every qualifying product footnotes the fetch's limit cut. */
export function TopProductsCard({
  data,
  loading,
  error,
}: {
  data: TopProducts | null;
  loading: boolean;
  error: Error | null;
}) {
  const [showAll, setShowAll] = useState(false);
  const { t } = useTranslation('dashboard');
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const inline = rows.slice(0, TOP_LIST_INLINE);
  const overflow = rows.length - inline.length;
  const isEmpty = !loading && !error && rows.length === 0;
  const totalSuffix = data
    ? t('lists.totalSuffix', { total: formatCents(data.total_spend_cents, currency) })
    : '';

  const rowEl = (row: (typeof rows)[number], i: number) => (
    <div key={row.product_id} className="insight-row">
      <span className="toplist-rank">{i + 1}</span>
      <div className="insight-body">
        <span className="insight-message">{row.name}</span>
        <span className="hint-inline">
          {t('topProducts.row.bought', { count: row.quantity })}
          {row.avg_price_cents > 0 ? ` · ${t('topProducts.row.avg', { avg: formatCents(row.avg_price_cents, currency) })}` : ''}
        </span>
      </div>
      <span className="insight-age">{formatCents(row.total_cents, currency)}</span>
    </div>
  );

  return (
    <ChartCard
      title={t('topProducts.title')}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      subtitle={data ? t('lists.bySpend', { total: formatCents(data.total_spend_cents, data.currency) }) : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage={t('topProducts.empty')}
    >
      <div className="insight-list">{inline.map(rowEl)}</div>
      {overflow > 0 && (
        <Button variant="ghost" onClick={() => setShowAll(true)}>
          {t('topProducts.viewAll', { count: rows.length })}
        </Button>
      )}
      <TopListDialog title={t('topProducts.dialogTitle')} open={showAll} onClose={() => setShowAll(false)}>
        <p className="hint-text">
          {t('topProducts.dialogText', { count: rows.length, suffix: totalSuffix })}
        </p>
        <div className="insight-list">{rows.map(rowEl)}</div>
      </TopListDialog>
    </ChartCard>
  );
}