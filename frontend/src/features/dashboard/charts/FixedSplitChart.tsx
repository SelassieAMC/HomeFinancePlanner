import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { useTranslation } from 'react-i18next';
import i18n from '../../../i18n';
import { intlLocale } from '../../../i18n/core';
import type { FixedSplit } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { ChartCard } from './ChartCard';
import { rangeLabel } from '../../../lib/date';
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

/** Stacked monthly cash flow: fixed commitments at the base, discretionary
 *  on top — the baseline cost of living versus flexible spending. */
export function FixedSplitChart({ data, loading, error }: Props) {
  const { t } = useTranslation('dashboard');
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
      title={t('fixedSplit.title')}
      subtitle={t('fixedSplit.subtitle')}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      loading={loading}
      error={error}
      emptyMessage={t('fixedSplit.empty')}
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
              name={t('fixedSplit.series.fixed')}
              stackId="1"
              stroke="var(--surface)"
              strokeWidth={2}
              fill="var(--violet)"
              isAnimationActive={false}
            />
            <Area
              type="monotone"
              dataKey="discretionary"
              name={t('fixedSplit.series.discretionary')}
              stackId="1"
              stroke="var(--surface)"
              strokeWidth={2}
              fill="var(--accent)"
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="chart-legend">
        <span><span className="chart-legend-swatch" style={{ background: 'var(--violet)' }} />{t('fixedSplit.series.fixed')}</span>
        <span><span className="chart-legend-swatch" style={{ background: 'var(--accent)' }} />{t('fixedSplit.series.discretionary')}</span>
        {latestPct !== null && <span>{t('fixedSplit.fixedShare', { count: latestPct })}</span>}
        {data !== null && data.unclassified_cents > 0 && (
          <span>{t('fixedSplit.unclassified', { amount: formatCents(data.unclassified_cents, currency) })}</span>
        )}
      </div>
    </ChartCard>
  );
}