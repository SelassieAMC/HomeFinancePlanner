import { useTranslation } from 'react-i18next';
import i18n from '../../../i18n';
import { intlLocale } from '../../../i18n/core';
import type { SpendHeatmap } from '../../../types/analytics';
import { formatCents } from '../../../lib/money';
import { heatmapShade } from '../../../lib/analytics';
import { ChartCard } from './ChartCard';
import { rangeLabel } from '../../../lib/date';

interface Props {
  data: SpendHeatmap | null;
  loading: boolean;
  error: Error | null;
}

const STEPS = 6;

/** Weekday × purchase-type grid. Color intensity = total spend; every cell
 *  also carries a tooltip so the value is never color-alone. */
export function SpendHeatmapChart({ data, loading, error }: Props) {
  const { t } = useTranslation('dashboard');
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const max = data?.max_cents ?? 0;
  // Monday-first short weekday names, localized via Intl of the active UI
  // language (Jan 2, 2023 is a Monday; +i walks Tue … Sun).
  const days = Array.from({ length: 7 }, (_, d) =>
    new Date(2023, 0, 2 + d).toLocaleDateString(intlLocale(i18n.language), { weekday: 'short' }),
  );
  return (
    <ChartCard
      title={t('heatmap.title')}
      subtitle={t('heatmap.subtitle')}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      loading={loading}
      error={error}
      emptyMessage={t('heatmap.empty')}
      empty={max <= 0}
    >
      <div className="heatmap" role="img" aria-label={t('heatmap.aria')}>
        <span className="heatmap-corner" aria-hidden="true" />
        {days.map((day) => (
          <span key={day} className="heatmap-day">
            {day}
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
                  title={`${row.group} · ${days[d]}: ${formatCents(cell.total_cents, currency)}`}
                />
              );
            })}
          </div>
        ))}
      </div>
      <div className="chart-legend">
        <span>{t('heatmap.less')}</span>
        {Array.from({ length: STEPS }, (_, i) => (
          <span
            key={i}
            className="chart-legend-swatch"
            style={{ background: i === 0 ? 'var(--seq-0)' : `var(--seq-${i})`, border: i === 0 ? '1px solid var(--border)' : 'none' }}
          />
        ))}
        <span>{t('heatmap.more')}</span>
      </div>
    </ChartCard>
  );
}