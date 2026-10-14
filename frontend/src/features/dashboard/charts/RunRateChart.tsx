import {
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useTranslation } from 'react-i18next';
import i18n from '../../../i18n';
import { intlLocale } from '../../../i18n/core';
import type { RunRate, RunRatePoint } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { ChartCard } from './ChartCard';
import { ChartTooltip } from './ChartTooltip';

interface Props {
  data: RunRate | null;
  loading: boolean;
  error: Error | null;
}

interface Row {
  day: number;
  /** undefined = no data → recharts renders a gap, not a zero. */
  current?: number;
  previous?: number;
  average?: number;
  pace?: number;
}

/** Recharts treats missing keys as gaps only when the field is undefined;
 *  null renders as a zero. Convert nulls to undefined so lines break instead
 *  of diving to the axis ("month so far" must not fall off a cliff). */
function toRows(points: RunRatePoint[]): Row[] {
  return points.map((p) => ({
    day: p.day,
    current: p.current_cents ?? undefined,
    previous: p.previous_cents ?? undefined,
    average: p.average_cents ?? undefined,
    pace: p.budget_pace_cents ?? undefined,
  }));
}

/** Cumulative month-to-date spend vs. last month, the 3-month average and a
 *  linear budget pace. Reference lines are dashed and muted; the current
 *  month is the only emphasized series. */
export function RunRateChart({ data, loading, error }: Props) {
  const { t } = useTranslation('dashboard');
  const rows = toRows(data?.points ?? []);
  const currency = data?.currency ?? '';
  const hasPace = data !== null && data.budget_total_cents > 0;
  return (
    <ChartCard
      title={t('runRate.title')}
      window={data && data.month ? monthTitle(data.month) : undefined}
      subtitle={t('runRate.subtitle')}
      loading={loading}
      error={error}
      emptyMessage={t('runRate.empty')}
      empty={rows.every((r) => r.current === undefined)}
    >
      <div className="chart-body">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart data={rows} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="day"
              tickLine={false}
              interval={2}
              tickFormatter={(d: number) => String(d)}
            />
            <YAxis
              tickLine={false}
              width={64}
              tickFormatter={(v: number) => formatCents(v, currency).replace(/,\d{2}$/, '')}
            />
            <Tooltip
              content={
                <ChartTooltip
                  title={(l) => t('runRate.tooltipDay', { count: l })}
                  format={(v) => formatCents(Number(v), currency)}
                />
              }
            />
            {hasPace && (
              <ReferenceLine y={data?.budget_total_cents} stroke="var(--chart-2)" strokeDasharray="2 4" opacity={0.6} />
            )}
            <Line
              type="monotone"
              dataKey="previous"
              name={t('runRate.series.lastMonth')}
              stroke="var(--text-muted)"
              strokeWidth={1.5}
              strokeDasharray="5 4"
              dot={false}
              connectNulls={false}
            />
            <Line
              type="monotone"
              dataKey="average"
              name={t('runRate.series.avg3')}
              stroke="var(--chart-4)"
              strokeWidth={1.5}
              strokeDasharray="5 4"
              dot={false}
              connectNulls={false}
            />
            <Line
              type="monotone"
              dataKey="pace"
              name={t('runRate.series.pace')}
              stroke="var(--chart-2)"
              strokeWidth={1.5}
              strokeDasharray="2 4"
              dot={false}
              connectNulls
            />
            <Line
              type="monotone"
              dataKey="current"
              name={t('runRate.series.thisMonth')}
              stroke="var(--chart-1)"
              strokeWidth={2.5}
              dot={false}
              connectNulls={false}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </div>
      <div className="chart-legend">
        <span><span className="chart-legend-swatch" style={{ background: 'var(--chart-1)' }} />{t('runRate.series.thisMonth')}</span>
        <span><span className="chart-legend-swatch" style={{ background: 'var(--text-muted)' }} />{t('runRate.series.lastMonth')}</span>
        <span><span className="chart-legend-swatch" style={{ background: 'var(--chart-4)' }} />{t('runRate.series.avg3')}</span>
        {hasPace && <span><span className="chart-legend-swatch" style={{ background: 'var(--chart-2)' }} />{t('runRate.series.pace')}</span>}
      </div>
    </ChartCard>
  );
}
/** "2026-10" → "October 2026" — the run-rate chart covers exactly one
 *  calendar month, so its window reads as a month, not a range. */
function monthTitle(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month);
  if (!m) return month;
  return new Date(Number(m[1]), Number(m[2]) - 1, 1).toLocaleDateString(intlLocale(i18n.language), {
    year: 'numeric',
    month: 'long',
  });
}
