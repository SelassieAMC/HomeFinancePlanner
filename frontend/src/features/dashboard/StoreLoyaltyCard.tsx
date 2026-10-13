import { ChartCard } from './charts/ChartCard';
import { formatCents } from '../../lib/money';
import type { TopStores } from '../../types/analytics';

/** Step-3 consumption card: the period's most shopped stores, ranked by
 *  converted spend with the visit count (accepted bills + manual expense
 *  transactions naming the store). */
export function StoreLoyaltyCard({
  data,
  loading,
  error,
}: {
  data: TopStores | null;
  loading: boolean;
  error: Error | null;
}) {
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const isEmpty = !loading && !error && rows.length === 0;

  return (
    <ChartCard
      title="Store Loyalty"
      subtitle={data ? `by spend, ${formatCents(data.total_spend_cents, data.currency)} total` : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage="No store-attributed purchases in this period."
    >
      <div className="insight-list">
        {rows.map((row) => {
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
        })}
      </div>
    </ChartCard>
  );
}