import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '../../components/ui';
import { ChartCard } from './charts/ChartCard';
import { TOP_LIST_INLINE, TopListDialog } from './TopProductsCard';
import { rangeLabel } from '../../lib/date';
import { formatCents } from '../../lib/money';
import type { TopStores } from '../../types/analytics';

/** Step-3 consumption card: the period's most shopped stores, ranked by
 *  converted spend with the visit count (accepted bills + manual expense
 *  transactions naming the store). Top 10 inline, the rest in a modal. */
export function StoreLoyaltyCard({
  data,
  loading,
  error,
}: {
  data: TopStores | null;
  loading: boolean;
  error: Error | null;
}) {
  const [showAll, setShowAll] = useState(false);
  const { t } = useTranslation('dashboard');
  const rows = data?.rows ?? [];
  const currency = data?.currency ?? '';
  const inline = rows.slice(0, TOP_LIST_INLINE);
  const overflow = rows.length - inline.length;
  const isEmpty = !loading && !error && rows.length === 0;
  const totalSuffix = data
    ? t('lists.totalSuffix', { total: formatCents(data.total_spend_cents, currency) })
    : '';

  const rowEl = (row: (typeof rows)[number]) => {
    const share =
      data && data.total_spend_cents > 0 ? Math.round((row.total_cents / data.total_spend_cents) * 100) : null;
    return (
      <div key={row.store_id} className="insight-row">
        <div className="insight-body">
          <span className="insight-message">{row.name}</span>
          <span className="hint-inline">
            {t('topStores.row.visits', { count: row.visit_count })}
            {share !== null ? ` · ${t('topStores.row.ofSpend', { share })}` : ''}
          </span>
        </div>
        <span className="insight-age">{formatCents(row.total_cents, currency)}</span>
      </div>
    );
  };

  return (
    <ChartCard
      title={t('topStores.title')}
      window={data ? rangeLabel(data.from, data.to) : undefined}
      subtitle={data ? t('lists.bySpend', { total: formatCents(data.total_spend_cents, data.currency) }) : undefined}
      loading={loading}
      error={error}
      empty={isEmpty}
      emptyMessage={t('topStores.empty')}
    >
      <div className="insight-list">{inline.map(rowEl)}</div>
      {overflow > 0 && (
        <Button variant="ghost" onClick={() => setShowAll(true)}>
          {t('topStores.viewAll', { count: rows.length })}
        </Button>
      )}
      <TopListDialog title={t('topStores.dialogTitle')} open={showAll} onClose={() => setShowAll(false)}>
        <p className="hint-text">
          {t('topStores.dialogText', { count: rows.length, suffix: totalSuffix })}
        </p>
        <div className="insight-list">{rows.map(rowEl)}</div>
      </TopListDialog>
    </ChartCard>
  );
}