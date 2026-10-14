import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { summaryApi } from '../../api/summary';
import { analyticsApi } from '../../api/analytics';
import { formatCents } from '../../lib/money';
import {
  isCurrentPeriod,
  monthOf,
  monthWindow,
  periodLabel,
  periodOf,
  shiftPeriod,
  todayISO,
  type Period,
  type PeriodType,
} from '../../lib/date';
import { Card, Tabs, type TabDef } from '../../components/ui';
import { PeriodSwitcher } from './PeriodSwitcher';
import { RunRateChart } from './charts/RunRateChart';
import { FixedSplitChart } from './charts/FixedSplitChart';
import { StorePriceIndexChart } from './charts/StorePriceIndexChart';
import { PersonalPriceIndexChart } from './charts/PersonalPriceIndexChart';
import { SpendSunburst } from './charts/SpendSunburst';
import { SpendHeatmapChart } from './charts/SpendHeatmap';
import { InsightsCard } from './InsightsCard';
import { ComingUpCard } from './ComingUpCard';
import { PriceCreepCard } from './PriceCreepCard';
import { AtAGlanceRow } from './AtAGlanceRow';
import { PlanningCard } from './PlanningCard';
import { TopProductsCard } from './TopProductsCard';
import { StoreLoyaltyCard } from './StoreLoyaltyCard';

// The dashboard's ranked consumption lists are fetched at the backend's hard
// cap (maxTopRows = 50) so the view-all modal can show the real tail; the
// cards display the top 10 inline.
const TOP_LIST_FETCH_LIMIT = 50;

type DashTab = 'planning' | 'products' | 'trends' | 'forecast';

export function DashboardPage() {
  const { t } = useTranslation('dashboard');
  const [type, setType] = useState<PeriodType>('month');
  // The anchor is any day inside the period; navigation shifts it by whole
  // period units. Starting from today, the dashboard opens on the current
  // period and can only move backwards — never into the future.
  const [anchor, setAnchor] = useState(todayISO());
  const [tab, setTab] = useState<DashTab>('planning');

  const period = periodOf(type, anchor);
  const month = monthOf(period.end);
  // The equal-length chunk before the shown one, for the spend tile's
  // comparison (a Monday–Sunday week vs. the week before, etc.).
  const prevPeriod: Period = shiftPeriod(period, -1);

  // Analytics windows derive from the period end so navigating to a past
  // month moves every chart consistently.
  const sixMonths = monthWindow(period.end, 5);
  const twelveMonths = monthWindow(period.end, 11);

  // Spend tiles: the shown period and its predecessor, period-scoped so the
  // Day/Week/Year switcher visibly changes the number.
  const periodSummary = useAsync(
    () => summaryApi.range(period.start, period.end),
    [period.start, period.end],
  );
  const prevSummary = useAsync(
    () => summaryApi.range(prevPeriod.start, prevPeriod.end),
    [prevPeriod.start, prevPeriod.end],
  );
  // Calendar-month summary for the planning card only — budgets are
  // calendar-month envelopes, so they never follow a day/week selection.
  const monthSummary = useAsync(() => summaryApi.month(month), [month]);
  // Chart payloads; each ChartCard renders its own loading/error/empty state.
  // The at-a-glance row is period-independent: fetched once, never re-fetched
  // when the period navigates.
  const glance = useAsync(() => summaryApi.glance(), []);
  const runRate = useAsync(() => analyticsApi.runRate(month), [month]);
  const fixedSplit = useAsync(() => analyticsApi.fixedSplit(sixMonths.from, sixMonths.to), [sixMonths.from, sixMonths.to]);
  const priceIndex = useAsync(() => analyticsApi.priceIndex(twelveMonths.from, twelveMonths.to), [twelveMonths.from, twelveMonths.to]);
  const storeIndex = useAsync(() => analyticsApi.storePriceIndex(period.start, period.end), [period.start, period.end]);
  const sunburst = useAsync(() => analyticsApi.categorySunburst(period.start, period.end), [period.start, period.end]);
  const heatmap = useAsync(() => analyticsApi.spendHeatmap(period.start, period.end), [period.start, period.end]);
  // Step-3 consumption analysis: period-scoped product/store rankings, fetched
  // at the ranked-list cap so the view-all modal has the real tail.
  const topProducts = useAsync(
    () => analyticsApi.topProducts(period.start, period.end, TOP_LIST_FETCH_LIMIT),
    [period.start, period.end],
  );
  const topStores = useAsync(
    () => analyticsApi.topStores(period.start, period.end, TOP_LIST_FETCH_LIMIT),
    [period.start, period.end],
  );

  // Currencies with no exchange rate are shown 1:1 — surface that once for
  // every chart that hit it.
  const warnings = [
    ...new Set(
      [glance.data, monthSummary.data, periodSummary.data, runRate.data, fixedSplit.data, priceIndex.data, storeIndex.data, sunburst.data, heatmap.data, topProducts.data, topStores.data].flatMap(
        (d) => d?.conversion_warnings ?? [],
      ),
    ),
  ];
  const conversionWarning =
    warnings.length > 0
      ? t('convert.warning', { codes: warnings.join(', ') })
      : '';

  // Spend tile comparison vs the previous equal-length period.
  const changePct = ((current?: number, previous?: number): number | null => {
    if (current === undefined || previous === undefined || previous <= 0) return null;
    return Math.round(((current - previous) / previous) * 100);
  })(periodSummary.data?.expense_cents, prevSummary.data?.expense_cents);

  // Fixed share and personal basket inflation are month-anchored trend stats.
  const splitMonths = fixedSplit.data?.months ?? [];
  const currentMonth = splitMonths.at(-1);
  const fixedPct =
    currentMonth && currentMonth.total_cents > 0
      ? Math.round((currentMonth.fixed_cents / currentMonth.total_cents) * 100)
      : null;
  const latestIndex = [...(priceIndex.data?.average ?? [])].reverse().find((p) => p.index !== null)?.index ?? null;
  const inflationPct = latestIndex !== null ? latestIndex - 100 : null;

  // Tab and tile labels translate at render time (call time, never module
  // load) via the hook's t().
  const tabs: TabDef<DashTab>[] = [
    { key: 'planning', label: t('tabs.planning') },
    { key: 'products', label: t('tabs.products') },
    { key: 'trends', label: t('tabs.trends') },
    { key: 'forecast', label: t('tabs.forecast') },
  ];
  // Tile-1 heading per period type: the label should say what the number
  // covers, so the picker's effect is visible at a glance.
  const tileLabel = ((tt: PeriodType) => {
    switch (tt) {
      case 'day':
        return t('tiles.spendDay');
      case 'week':
        return t('tiles.spendWeek');
      case 'month':
        return t('tiles.mtd');
      case 'year':
        return t('tiles.ytd');
    }
  })(type);
  // Noun for the comparison line vs. the equal-length period before.
  const periodNoun = ((tt: PeriodType) => {
    switch (tt) {
      case 'day':
        return t('tiles.nouns.day');
      case 'week':
        return t('tiles.nouns.week');
      case 'month':
        return t('tiles.nouns.month');
      case 'year':
        return t('tiles.nouns.year');
    }
  })(type);

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>
      <PeriodSwitcher
        type={type}
        label={periodLabel(period)}
        nextDisabled={isCurrentPeriod(period)}
        onTypeChange={(pt) => {
          setType(pt);
          // Keep "today" as the reference so switching always lands on the
          // current period, not wherever the previous type had navigated.
          setAnchor(todayISO());
        }}
        onPrev={() => setAnchor(shiftPeriod(period, -1).start)}
        onNext={() => setAnchor(shiftPeriod(period, 1).start)}
      />

      {conversionWarning && <div className="bill-warning">{conversionWarning}</div>}

      {/* Always-on-top zone: current standing (period-independent), the live
          nudges (hidden when empty) and the stat tiles (tile 1 follows the
          picker, tiles 2–3 state their own window). */}
      <AtAGlanceRow glance={glance} />

      {/* Deferred intelligence: unread price-per-unit nudges the analysis job
          wrote after recent purchases. Hidden entirely when none are unread. */}
      <InsightsCard />

      {/* The scheduler's due-and-overdue occurrences: the confirm-first nudge
          with one-click apply. Hidden entirely when nothing is due. */}
      <ComingUpCard />

      <div className="stat-grid">
        <Card className="stat-tile">
          <span className="stat-label">{tileLabel}</span>
          <span className="stat-value">
            {periodSummary.loading ? '…' : formatCents(periodSummary.data?.expense_cents ?? 0, periodSummary.data?.currency ?? '')}
          </span>
          <span className="stat-label">
            {changePct === null
              ? t('tiles.noCompare', { noun: periodNoun })
              : t('tiles.compare', { pct: `${changePct > 0 ? '+' : ''}${changePct}`, noun: periodNoun })}
          </span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">{t('tiles.fixedTitle')}</span>
          <span className="stat-value">{fixedPct === null ? '…' : t('tiles.fixedValue', { count: fixedPct })}</span>
          <span className="stat-label">
            {fixedPct === null
              ? t('tiles.noData')
              : t('tiles.discValue', { count: 100 - fixedPct, month: monthLabel(month) })}
          </span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">{t('tiles.basketTitle')}</span>
          <span className="stat-value">
            {inflationPct === null ? '…' : `${inflationPct > 0 ? '+' : ''}${inflationPct.toFixed(1)}%`}
          </span>
          <span className="stat-label">{inflationPct === null ? t('tiles.noData') : t('tiles.vsLastYear')}</span>
        </Card>
      </div>

      {/* Tabbed sections: planning layers, product analysis, trend charts and
          the forecast. One pane open at a time keeps the page height constant. */}
      <Tabs tabs={tabs} active={tab} onChange={setTab} ariaLabel={t('aria.sections')} />

      {tab === 'planning' && <PlanningCard summary={monthSummary} />}

      {tab === 'products' && (
        // Step-3 consumption section: what was bought, where, and how the
        // spend breaks down by category.
        <div className="chart-grid-2">
          <TopProductsCard data={topProducts.data} loading={topProducts.loading} error={topProducts.error} />
          <StoreLoyaltyCard data={topStores.data} loading={topStores.loading} error={topStores.error} />
          <SpendSunburst data={sunburst.data} loading={sunburst.loading} error={sunburst.error} />
          <PriceCreepCard />
        </div>
      )}

      {tab === 'trends' && (
        <div className="chart-grid-2">
          <SpendHeatmapChart data={heatmap.data} loading={heatmap.loading} error={heatmap.error} />
          <FixedSplitChart data={fixedSplit.data} loading={fixedSplit.loading} error={fixedSplit.error} />
          <StorePriceIndexChart data={storeIndex.data} loading={storeIndex.loading} error={storeIndex.error} />
          <PersonalPriceIndexChart data={priceIndex.data} loading={priceIndex.loading} error={priceIndex.error} />
        </div>
      )}

      {tab === 'forecast' && (
        <div className="chart-grid-2">
          {/* Cumulative spend vs. last month / budget pace for the shown
              period's calendar month. */}
          <RunRateChart data={runRate.data} loading={runRate.loading} error={runRate.error} />
        </div>
      )}
    </div>
  );
}

/** "2026-10" → "Oct 2026" for the fixed-split tile's window hint. */
function monthLabel(month: string): string {
  const m = /^(\d{4})-(\d{2})$/.exec(month);
  if (!m) return month;
  return new Date(Number(m[1]), Number(m[2]) - 1, 1).toLocaleString(intlLocale(i18n.language), {
    month: 'short',
  });
}