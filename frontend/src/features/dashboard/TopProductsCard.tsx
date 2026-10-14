import { useState, type ReactNode } from 'react';
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
  if (!open) return null;
  return (
    <Dialog title={title} wide>
      {children}
      <div className="dialog-actions">
        <Button variant="secondary" onClick={onClose}>
          Close
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
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const inline = rows.slice(0, TOP_LIST_INLINE);
  const overflow = rows.length - inline.length;
  const isEmpty = !loading && !error && rows.length === 0;

  const rowEl = (row: (typeof rows)[number], i: number) => (
    <div key={row.product_id} className="insight-row">
      <span className="toplist-rank">{i + 1}</span>
      <div className="insight-body">
        <span className="insight-message">{row.name}</span>
        <span className="hint-inline">
          {formatQuantity(row.quantity)} bought
          {row.avg_price_cents > 0 ? ` · ${formatCents(row.avg_price_cents, currency)} avg` : ''}
        </span>
      </div>
      <span className="insight-age">{formatCents(row.total_cents, currency)}</span>
    </div>
  );

  return (
    <ChartCard
      title="Top Products"
      window={data ? rangeLabel(data.from, data.to) : undefined}
      subtitle={data ? `by spend, ${formatCents(data.total_spend_cents, data.currency)} total` : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage="No product-linked purchases in this period."
    >
      <div className="insight-list">{inline.map(rowEl)}</div>
      {overflow > 0 && (
        <Button variant="ghost" onClick={() => setShowAll(true)}>
          View all {rows.length} products
        </Button>
      )}
      <TopListDialog title="Top Products — full list" open={showAll} onClose={() => setShowAll(false)}>
        <p className="hint-text">
          All {rows.length} products with purchases in this period, ranked by spend
          {data ? ` — ${formatCents(data.total_spend_cents, currency)} total` : ''}.
        </p>
        <div className="insight-list">{rows.map(rowEl)}</div>
      </TopListDialog>
    </ChartCard>
  );
}

/** Quantities are line sums and read better without trailing zeros. */
function formatQuantity(q: number): string {
  return q % 1 === 0 ? q.toFixed(0) : q.toFixed(1);
}