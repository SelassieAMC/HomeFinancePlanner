import { useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { summaryApi } from '../../api/summary';
import { analyticsApi } from '../../api/analytics';
import { formatCents } from '../../lib/money';
import { isCurrentPeriod, monthOf, monthWindow, periodLabel, periodOf, shiftPeriod, todayISO, type PeriodType } from '../../lib/date';
import { Card } from '../../components/ui';
import { PeriodSwitcher } from './PeriodSwitcher';
import { RunRateChart } from './charts/RunRateChart';
import { FixedSplitChart } from './charts/FixedSplitChart';
import { StorePriceIndexChart } from './charts/StorePriceIndexChart';
import { PersonalPriceIndexChart } from './charts/PersonalPriceIndexChart';
import { SpendSunburst } from './charts/SpendSunburst';
import { SpendHeatmapChart } from './charts/SpendHeatmap';

export function DashboardPage() {
  const [type, setType] = useState<PeriodType>('month');
  // The anchor is any day inside the period; navigation shifts it by whole
  // period units. Starting from today, the dashboard opens on the current
  // period and can only move backwards — never into the future.
  const [anchor, setAnchor] = useState(todayISO());

  const period = periodOf(type, anchor);
  const month = monthOf(period.end);

  // Analytics windows derive from the period end so navigating to a past
  // month moves every chart consistently.
  const sixMonths = monthWindow(period.end, 5);
  const twelveMonths = monthWindow(period.end, 11);

  // Month-to-date tile: the summary for the anchor's calendar month.
  const monthSummary = useAsync(() => summaryApi.month(month), [month]);
  // Chart payloads; each ChartCard renders its own loading/error/empty state.
  const runRate = useAsync(() => analyticsApi.runRate(month), [month]);
  const fixedSplit = useAsync(() => analyticsApi.fixedSplit(sixMonths.from, sixMonths.to), [sixMonths.from, sixMonths.to]);
  const priceIndex = useAsync(() => analyticsApi.priceIndex(twelveMonths.from, twelveMonths.to), [twelveMonths.from, twelveMonths.to]);
  const storeIndex = useAsync(() => analyticsApi.storePriceIndex(period.start, period.end), [period.start, period.end]);
  const sunburst = useAsync(() => analyticsApi.categorySunburst(period.start, period.end), [period.start, period.end]);
  const heatmap = useAsync(() => analyticsApi.spendHeatmap(period.start, period.end), [period.start, period.end]);

  // Currencies with no exchange rate are shown 1:1 — surface that once for
  // every chart that hit it.
  const warnings = [
    ...new Set(
      [runRate.data, fixedSplit.data, priceIndex.data, storeIndex.data, sunburst.data, heatmap.data].flatMap(
        (d) => d?.conversion_warnings ?? [],
      ),
    ),
  ];
  const conversionWarning =
    warnings.length > 0
      ? `Exchange rate missing for ${warnings.join(', ')} — those amounts are shown 1:1.`
      : '';

  // Stat tiles, following the mockup: month-to-date with month-over-month
  // change, the fixed share, and personal basket inflation.
  const splitMonths = fixedSplit.data?.months ?? [];
  const currentMonth = splitMonths.at(-1);
  const prevMonth = splitMonths.at(-2);
  const momPct =
    currentMonth && prevMonth && prevMonth.total_cents > 0
      ? Math.round(((currentMonth.total_cents - prevMonth.total_cents) / prevMonth.total_cents) * 100)
      : null;
  const fixedPct =
    currentMonth && currentMonth.total_cents > 0
      ? Math.round((currentMonth.fixed_cents / currentMonth.total_cents) * 100)
      : null;
  const latestIndex = [...(priceIndex.data?.average ?? [])].reverse().find((p) => p.index !== null)?.index ?? null;
  const inflationPct = latestIndex !== null ? latestIndex - 100 : null;

  return (
    <div className="page">
      <h2 className="page-title">Dashboard</h2>
      <PeriodSwitcher
        type={type}
        label={periodLabel(period)}
        nextDisabled={isCurrentPeriod(period)}
        onTypeChange={(t) => {
          setType(t);
          // Keep "today" as the reference so switching always lands on the
          // current period, not wherever the previous type had navigated.
          setAnchor(todayISO());
        }}
        onPrev={() => setAnchor(shiftPeriod(period, -1).start)}
        onNext={() => setAnchor(shiftPeriod(period, 1).start)}
      />

      {conversionWarning && <div className="bill-warning">{conversionWarning}</div>}

      <div className="stat-grid">
        <Card className="stat-tile">
          <span className="stat-label">Month-to-Date</span>
          <span className="stat-value">{monthSummary.loading ? '…' : formatCents(monthSummary.data?.expense_cents ?? 0, monthSummary.data?.currency ?? '')}</span>
          <span className="stat-label">
            {momPct === null ? 'no comparison yet' : `${momPct > 0 ? '+' : ''}${momPct}% vs last month`}
          </span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">Fixed vs. Discretionary</span>
          <span className="stat-value">{fixedPct === null ? '…' : `${fixedPct}% Fixed`}</span>
          <span className="stat-label">{fixedPct === null ? 'no data yet' : `${100 - fixedPct}% Discretionary`}</span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">Personal Basket Inflation</span>
          <span className="stat-value">
            {inflationPct === null ? '…' : `${inflationPct > 0 ? '+' : ''}${inflationPct.toFixed(1)}%`}
          </span>
          <span className="stat-label">{inflationPct === null ? 'no data yet' : '12 months'}</span>
        </Card>
      </div>

      <div className="chart-grid-2">
        <RunRateChart data={runRate.data} loading={runRate.loading} error={runRate.error} />
        <FixedSplitChart data={fixedSplit.data} loading={fixedSplit.loading} error={fixedSplit.error} />
        <SpendHeatmapChart data={heatmap.data} loading={heatmap.loading} error={heatmap.error} />
        <SpendSunburst data={sunburst.data} loading={sunburst.loading} error={sunburst.error} />
        <StorePriceIndexChart data={storeIndex.data} loading={storeIndex.loading} error={storeIndex.error} />
        <PersonalPriceIndexChart data={priceIndex.data} loading={priceIndex.loading} error={priceIndex.error} />
      </div>
    </div>
  );
}
