import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useTranslation } from 'react-i18next';
import type { StorePriceIndex, StorePriceIndexRow } from '../../../types/analytics';
import { ChartCard } from './ChartCard';
import { rangeLabel } from '../../../lib/date';
import { ChartTooltip } from './ChartTooltip';

interface Props {
  data: StorePriceIndex | null;
  loading: boolean;
  error: Error | null;
}

/** Horizontal bars, one per store, cheapest first. The x-axis starts at 0
 *  (truncating a bar axis distorts the comparison); the 100 reference line
 *  marks the overall average. One hue — rank is shown by position, and the
 *  tooltip carries the sample size. */
export function StorePriceIndexChart({ data, loading, error }: Props) {
  const { t } = useTranslation('dashboard');
  const rows: StorePriceIndexRow[] = data?.rows ?? [];
  return (
    <ChartCard
      title={t('storePriceIndex.title')}
      subtitle={t('storePriceIndex.subtitle')}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      loading={loading}
      error={error}
      emptyMessage={t('storePriceIndex.empty')}
      empty={rows.length === 0}
    >
      <div className="chart-body">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={rows} layout="vertical" margin={{ top: 4, right: 44, left: 0, bottom: 0 }}>
            <CartesianGrid horizontal={false} strokeDasharray="3 3" />
            <XAxis
              type="number"
              domain={[0, 'auto']}
              tickFormatter={(v: number) => `${v}%`}
              allowDecimals={false}
            />
            <YAxis type="category" dataKey="store_name" width={72} />
            <Tooltip
              cursor={{ fill: 'var(--border)', opacity: 0.4 }}
              content={
                <ChartTooltip
                  format={(v, e) => {
                    const row = e.payload as StorePriceIndexRow | undefined;
                    return `${Number(v).toFixed(1)}% · ${t('storePriceIndex.tooltip.products', { count: row?.product_count ?? 0 })}`;
                  }}
                />
              }
            />
            <ReferenceLine x={100} stroke="var(--text-muted)" strokeDasharray="4 4" />
            <Bar dataKey="index" name={t('storePriceIndex.series.priceIndex')} radius={[0, 4, 4, 0]} maxBarSize={22}>
              {rows.map((row) => (
                <Cell key={row.store_id} fill="var(--chart-1)" />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
      <div className="chart-legend">
        <span>
          <span className="chart-legend-swatch" style={{ background: 'var(--text-muted)' }} />
          {t('storePriceIndex.legend')}
        </span>
      </div>
    </ChartCard>
  );
}