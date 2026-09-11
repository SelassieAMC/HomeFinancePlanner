import type { ChartBucket } from '../../lib/date';
import { formatCents } from '../../lib/money';

// ExpensesChart renders one bar per bucket (a day for week/month periods, a
// month for year periods) using plain divs — responsive, no chart library.
// Buckets without expenses render as empty slots so the axis stays aligned.
// The bucket list is built by expenseBuckets in lib/date.
export function ExpensesChart({ buckets, currency }: { buckets: ChartBucket[]; currency: string }) {
  const max = Math.max(...buckets.map((b) => b.cents), 1);
  // Label roughly every 5th bucket when there are many (month view);
  // sparser bucket lists (week: 7, year: 12) label every slot.
  const labelEvery = buckets.length > 16 ? 5 : 1;

  return (
    <div className="bar-chart" role="img" aria-label="Expenses chart">
      <div className="bar-chart-plot">
        {buckets.map((b, i) => (
          <div className="bar-chart-col" key={`${b.title}-${i}`}>
            <div
              className={b.cents > 0 ? 'bar-chart-bar' : 'bar-chart-bar empty'}
              style={{ height: `${Math.max((b.cents / max) * 100, b.cents > 0 ? 4 : 0)}%` }}
              title={`${b.title}: ${formatCents(b.cents, currency)}`}
            />
          </div>
        ))}
      </div>
      <div className="bar-chart-axis">
        {buckets.map((b, i) => (
          <span key={`${b.title}-axis-${i}`}>{i % labelEvery === 0 ? b.label : ''}</span>
        ))}
      </div>
    </div>
  );
}