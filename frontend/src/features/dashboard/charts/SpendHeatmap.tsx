import type { SpendHeatmap } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { heatmapShade } from '../../../lib/analytics';
import { ChartCard } from './ChartCard';

interface Props {
  data: SpendHeatmap | null;
  loading: boolean;
  error: Error | null;
}

const DAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const STEPS = 6;

/** Weekday × purchase-type grid. Color intensity = total spend; every cell
 *  also carries a tooltip so the value is never color-alone. */
export function SpendHeatmapChart({ data, loading, error }: Props) {
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const max = data?.max_cents ?? 0;
  return (
    <ChartCard
      title="Shopping Habits Heatmap"
      subtitle="Accepted bill lines by weekday and purchase type."
      loading={loading}
      error={error}
      emptyMessage="No accepted bills in this period yet."
      empty={max <= 0}
    >
      <div className="heatmap" role="img" aria-label="Spending by weekday and purchase type">
        <span className="heatmap-corner" aria-hidden="true" />
        {DAYS.map((d) => (
          <span key={d} className="heatmap-day">
            {d}
          </span>
        ))}
        {rows.map((row) => (
          <div key={row.group} style={{ display: 'contents' }}>
            <span className="heatmap-label">{row.group}</span>
            {row.cells.map((cell, d) => {
              const step = heatmapShade(cell.total_cents, max, STEPS);
              return (
                <div
                  key={d}
                  className="heatmap-cell"
                  style={step > 0 ? { background: `var(--seq-${step})`, border: 'none' } : undefined}
                  title={`${row.group} · ${DAYS[d]}: ${formatCents(cell.total_cents, currency)}`}
                />
              );
            })}
          </div>
        ))}
      </div>
      <div className="chart-legend">
        <span>less</span>
        {Array.from({ length: STEPS }, (_, i) => (
          <span
            key={i}
            className="chart-legend-swatch"
            style={{ background: i === 0 ? 'var(--seq-0)' : `var(--seq-${i})`, border: i === 0 ? '1px solid var(--border)' : 'none' }}
          />
        ))}
        <span>more</span>
      </div>
    </ChartCard>
  );
}