import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  XAxis,
  YAxis,
} from 'recharts';
import { ChartTooltip } from './ChartTooltip';
import type { PriceIndex } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { priceIndexRows } from '../../../lib/analytics';
import { ChartCard } from './ChartCard';

interface Props {
  data: PriceIndex | null;
  loading: boolean;
  error: Error | null;
}

const MONTH_LABELS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function monthLabel(month: string | number): string {
  const s = String(month);
  const m = Number(s.slice(5, 7));
  return `${MONTH_LABELS[m - 1] ?? s} ${s.slice(2, 4)}`;
}

/** Median unit price of the top staple items, indexed to 100 at each series'
 *  first month — a personal CPI. The average is a heavier solid line; months
 *  without purchases break the line instead of dropping to the axis. */
export function PersonalPriceIndexChart({ data, loading, error }: Props) {
  const { rows, labels, unitPrices, units } = priceIndexRows(
    data ?? { from: '', to: '', base_month: '', currency: '', series: [], average: [], conversion_warnings: [] },
  );
  const currency = data?.currency ?? '';
  const latest = [...(data?.average ?? [])].reverse().find((p) => p.index !== null)?.index ?? null;

  // Tooltip: index first, the absolute price when the month has one.
  const tooltip = (
    <ChartTooltip
      title={monthLabel}
      format={(v, entry, label) => {
        const name = String(entry.name ?? '');
        const unit = units.get(name);
        const price = unitPrices.get(`${name}|${label ?? ''}`);
        const index = Number(v).toFixed(1);
        return price !== undefined && unit
          ? `${index} · ${formatCents(price, currency)}/${unit}`
          : index;
      }}
    />
  );

  return (
    <ChartCard
      title="Personal Consumer Price Index"
      subtitle={`Unit prices of your most-bought staples, indexed to 100 at ${data ? monthLabel(data.base_month) : 'the base month'}.`}
      loading={loading}
      error={error}
      emptyMessage="No staple items purchased regularly in the last 12 months."
      empty={data === null || data.series.length === 0}
    >
      <div className="chart-body">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={rows} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="month"
              tickLine={false}
              tickFormatter={monthLabel}
              interval="preserveStartEnd"
              minTickGap={24}
            />
            <YAxis tickLine={false} width={44} domain={['auto', 'auto']} />
            {tooltip}
            <Legend formatter={(value) => <span style={{ color: 'var(--text-muted)', fontSize: 11 }}>{String(value)}</span>} />
            <ReferenceLine y={100} stroke="var(--text-muted)" strokeDasharray="4 4" opacity={0.6} />
            {labels.map((name, i) => {
              const isAverage = name === 'Average';
              return (
                <Line
                  key={name}
                  type="monotone"
                  dataKey={name}
                  name={name}
                  stroke={isAverage ? 'var(--text)' : `var(--chart-${(i % 8) + 1})`}
                  strokeWidth={isAverage ? 2.5 : 1.5}
                  dot={false}
                  connectNulls={false}
                  isAnimationActive={false}
                />
              );
            })}
          </LineChart>
        </ResponsiveContainer>
      </div>
      {latest !== null && (
        <div className="chart-legend">
          <span>
            Latest average index: <strong>{latest.toFixed(1)}</strong>
            {latest > 105 ? ' — prices rising' : latest < 95 ? ' — prices falling' : ' — stable'}
          </span>
        </div>
      )}
    </ChartCard>
  );
}