import { useState } from 'react';
import { Button } from '../../components/ui';
import { ChartCard } from './charts/ChartCard';
import { TOP_LIST_INLINE, TopListDialog } from './TopProductsCard';
import { formatCents } from '../../lib/money';
import type { TopStores } from '../../types/analytics';

/** Step-3 consumption card: the period's most shopped stores, ranked by
 *  converted spend with the visit count (accepted bills + manual expense
 *  transactions naming the store). Top 10 inline, the rest in a modal. */
export function StoreLoyaltyCard({
  data,
  loading,
  error,
}: {
  data: TopStores | null;
  loading: boolean;
  error: Error | null;
}) {
  const [showAll, setShowAll] = useState(false);
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const inline = rows.slice(0, TOP_LIST_INLINE);
  const overflow = rows.length - inline.length;
  const isEmpty = !loading && !error && rows.length === 0;

  const rowEl = (row: (typeof rows)[number]) => {
    const share =
      data && data.total_spend_cents > 0 ? Math.round((row.total_cents / data.total_spend_cents) * 100) : null;
    return (
      <div key={row.store_id} className="insight-row">
        <div className="insight-body">
          <span className="insight-message">{row.name}</span>
          <span className="hint-inline">
            {row.visit_count} {row.visit_count === 1 ? 'visit' : 'visits'}
            {share !== null ? ` · ${share}% of spend` : ''}
          </span>
        </div>
        <span className="insight-age">{formatCents(row.total_cents, currency)}</span>
      </div>
    );
  };

  return (
    <ChartCard
      title="Store Loyalty"
      subtitle={data ? `by spend, ${formatCents(data.total_spend_cents, data.currency)} total` : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage="No store-attributed purchases in this period."
    >
      <div className="insight-list">{inline.map(rowEl)}</div>
      {overflow > 0 && (
        <Button variant="ghost" onClick={() => setShowAll(true)}>
          View all {rows.length} stores →
        </Button>
      )}
      <TopListDialog title="Store Loyalty — full list" open={showAll} onClose={() => setShowAll(false)}>
        <p className="hint-text">
          All {rows.length} stores you bought from in this period, ranked by spend
          {data ? ` — ${formatCents(data.total_spend_cents, currency)} total` : ''}.
        </p>
        <div className="insight-list">{rows.map(rowEl)}</div>
      </TopListDialog>
    </ChartCard>
  );
}