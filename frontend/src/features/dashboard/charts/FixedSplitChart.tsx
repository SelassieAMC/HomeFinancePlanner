import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { FixedSplit } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { ChartCard } from './ChartCard';
import { ChartTooltip } from './ChartTooltip';

interface Props {
  data: FixedSplit | null;
  loading: boolean;
  error: Error | null;
}

interface Row {
  month: string;
  fixed: number;
  discretionary: number;
}

const MONTH_LABELS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function monthLabel(month: string | number): string {
  const s = String(month);
  const m = Number(s.slice(5, 7));
  return `${MONTH_LABELS[m - 1] ?? s} ${s.slice(2, 4)}`;
}

/** Stacked monthly cash flow: fixed commitments at the base, discretionary
 *  on top — the baseline cost of living versus flexible spending. */
export function FixedSplitChart({ data, loading, error }: Props) {
  const rows: Row[] = (data?.months ?? []).map((m) => ({
    month: monthLabel(m.month),
    fixed: m.fixed_cents,
    discretionary: m.discretionary_cents,
  }));
  const currency = data?.currency ?? '';
  const latest = data?.months.at(-1);
  const latestPct =
    latest && latest.total_cents > 0 ? Math.round((latest.fixed_cents / latest.total_cents) * 100) : null;
  return (
    <ChartCard
      title="Fixed vs. Discretionary"
      subtitle="Monthly spending split into fixed commitments (rent, utilities, insurance) and flexible spend."
      loading={loading}
      error={error}
      emptyMessage="No expenses recorded in this window yet."
      empty={rows.every((r) => r.fixed === 0 && r.discretionary === 0)}
    >
      <div className="chart-body">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={rows} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} />
            <XAxis dataKey="month" tickLine={false} />
            <YAxis
              tickLine={false}
              width={64}
              tickFormatter={(v: number) => formatCents(v, currency).replace(/,\d{2}$/, '')}
            />
            <Tooltip
              content={
                <ChartTooltip
                  format={(v) => formatCents(Number(v), currency)}
                />
              }
            />
            {/* Surface-colored strokes keep a 2px seam between stacked layers. */}
            <Area
              type="monotone"
              dataKey="fixed"
              name="Fixed"
              stackId="1"
              stroke="var(--surface)"
              strokeWidth={2}
              fill="var(--violet)"
              isAnimationActive={false}
            />
            <Area
              type="monotone"
              dataKey="discretionary"
              name="Discretionary"
              stackId="1"
              stroke="var(--surface)"
              strokeWidth={2}
              fill="var(--gold)"
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="chart-legend">
        <span><span className="chart-legend-swatch" style={{ background: 'var(--violet)' }} />Fixed</span>
        <span><span className="chart-legend-swatch" style={{ background: 'var(--gold)' }} />Discretionary</span>
        {latestPct !== null && <span>{latestPct}% fixed this month</span>}
        {data !== null && data.unclassified_cents > 0 && (
          <span>
            {formatCents(data.unclassified_cents, currency)} uncategorized (counted as discretionary)
          </span>
        )}
      </div>
    </ChartCard>
  );
}