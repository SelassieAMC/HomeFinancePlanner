import { ChartCard } from './charts/ChartCard';
import { formatCents } from '../../lib/money';
import type { TopProducts } from '../../types/analytics';

/** Step-3 consumption card: the period's most expensive products, ranked by
 *  converted spend with the bought quantity and the average price per piece.
 *  The total of every qualifying product footnotes the ?limit= cut. */
export function TopProductsCard({
  data,
  loading,
  error,
}: {
  data: TopProducts | null;
  loading: boolean;
  error: Error | null;
}) {
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const isEmpty = !loading && !error && rows.length === 0;

  return (
    <ChartCard
      title="Top Products"
      subtitle={data ? `by spend, ${formatCents(data.total_spend_cents, data.currency)} total` : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage="No product-linked purchases in this period."
    >
      <div className="insight-list">
        {rows.map((row, i) => (
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
        ))}
      </div>
    </ChartCard>
  );
}

/** Quantities are line sums and read better without trailing zeros. */
function formatQuantity(q: number): string {
  return q % 1 === 0 ? q.toFixed(0) : q.toFixed(1);
}