import { useTranslation } from 'react-i18next';
import { Card, ErrorMessage, Spinner } from '../../components/ui';
import { insightsApi } from '../../api/insights';
import { useAsync } from '../../hooks/useAsync';
import { formatCents } from '../../lib/money';
import type { ProductInsight } from '../../types/domain';

/** Step-3 price-creep tracker: the unread price-per-unit creep warnings,
 *  de-duplicated so a product re-warning shows once. Rows acknowledged
 *  (dismissed) stay hidden wherever they were dismissed. Hidden entirely
 *  when nothing qualifies — unlike the chart cards, an empty watch list is
 *  not a card. */
export function PriceCreepCard() {
  const { t } = useTranslation('dashboard');
  const creep = useAsync(() => insightsApi.list({ unseen: true, kind: 'price_creep', limit: 10 }), []);

  /** The old → new per-unit line when the facts snapshot carries both sides. */
  const creepTrend = (insight: ProductInsight): string => {
    const d = insight.data;
    if (d.old_ppu !== undefined && d.new_ppu !== undefined) {
      return t('facts.ppu', {
        unit: d.unit,
        old: formatCents(Math.round(d.old_ppu), insight.currency),
        new: formatCents(Math.round(d.new_ppu), insight.currency),
      });
    }
    if (d.old_price_cents !== undefined && d.new_price_cents !== undefined) {
      return t('facts.unitPrice', {
        old: formatCents(d.old_price_cents, insight.currency),
        new: formatCents(d.new_price_cents, insight.currency),
      });
    }
    return '';
  };

  if (creep.loading) {
    return (
      <Card title={t('priceCreep.title')} className="insights-card">
        <Spinner />
      </Card>
    );
  }
  if (creep.error) {
    return (
      <Card title={t('priceCreep.title')} className="insights-card">
        <ErrorMessage message={creep.error.message} />
      </Card>
    );
  }

  // Latest finding per product (the API lists newest first) and a trend line
  // from the saved facts.
  const byProduct = new Map<string, ProductInsight>();
  for (const insight of creep.data ?? []) {
    const key = insight.product_id !== null && insight.product_id !== undefined ? `p${insight.product_id}` : insight.product_name;
    if (!byProduct.has(key)) byProduct.set(key, insight);
  }
  const rows = [...byProduct.values()];
  if (rows.length === 0) return null;

  return (
    <Card title={t('priceCreep.title')} className="insights-card">
      <p className="chart-subtitle">{t('priceCreep.subtitle')}</p>
      <div className="insight-list">
        {rows.map((insight) => (
          <div key={insight.id} className="insight-row">
            <div className="insight-body">
              <span className="insight-message">
                <strong>{insight.product_name}</strong>
              </span>
              {creepTrend(insight) && <span className="hint-inline">{creepTrend(insight)}</span>}
            </div>
            <span className="insight-age badge badge-insight-watch">+{insight.data.change_pct.toFixed(0)}%</span>
          </div>
        ))}
      </div>
    </Card>
  );
}