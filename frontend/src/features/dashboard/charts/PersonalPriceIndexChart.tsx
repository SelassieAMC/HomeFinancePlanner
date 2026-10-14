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
import { useTranslation } from 'react-i18next';
import i18n from '../../../i18n';
import { intlLocale } from '../../../i18n/core';
import { ChartTooltip } from './ChartTooltip';
import type { PriceIndex } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { priceIndexRows } from '../../../lib/analytics';
import { ChartCard } from './ChartCard';
import { rangeLabel } from '../../../lib/date';

interface Props {
  data: PriceIndex | null;
  loading: boolean;
  error: Error | null;
}

/** "2026-10" → "Oct 26" — month name localized via Intl of the active UI
 *  language (a malformed label stays as it came). */
function monthLabel(month: string | number): string {
  const s = String(month);
  const m = /^(\d{4})-(\d{2})/.exec(s);
  if (!m) return s;
  const name = new Date(Number(m[1]), Number(m[2] ?? 1) - 1, 1).toLocaleDateString(
    intlLocale(i18n.language),
    { month: 'short' },
  );
  return `${name} ${s.slice(2, 4)}`;
}

/** Median unit price of the top staple items, indexed to 100 at each series'
 *  first month — a personal CPI. The average is a heavier solid line; months
 *  without purchases break the line instead of dropping to the axis. */
export function PersonalPriceIndexChart({ data, loading, error }: Props) {
  const { t } = useTranslation('dashboard');
  const { rows, labels, unitPrices, units } = priceIndexRows(
    data ?? { from: '', to: '', base_month: '', currency: '', series: [], average: [], conversion_warnings: [] },
  );
  const currency = data?.currency ?? '';
  const latest = [...(data?.average ?? [])].reverse().find((p) => p.index !== null)?.index ?? null;

  // The 'Average' series' row key stays the internal 'Average'; only the
  // display name (line name, legend, tooltip) is translated.
  const averageLabel = t('priceIndex.series.average');
  // Tooltip: index first, the absolute price when the month has one.
  const tooltip = (
    <ChartTooltip
      title={monthLabel}
      format={(v, entry, label) => {
        const name = String(entry.name ?? '') === averageLabel ? 'Average' : String(entry.name ?? '');
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
      title={t('priceIndex.title')}
      subtitle={t('priceIndex.subtitle', {
        base: data ? monthLabel(data.base_month) : t('priceIndex.baseFallback'),
      })}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      loading={loading}
      error={error}
      emptyMessage={t('priceIndex.empty')}
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
                  name={isAverage ? averageLabel : name}
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
            {t('priceIndex.latest.prefix')} <strong>{latest.toFixed(1)}</strong>{' '}
            {latest > 105 ? t('priceIndex.latest.rising') : latest < 95 ? t('priceIndex.latest.falling') : t('priceIndex.latest.stable')}
          </span>
        </div>
      )}
    </ChartCard>
  );
}