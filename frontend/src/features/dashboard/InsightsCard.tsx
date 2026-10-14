import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, ErrorMessage } from '../../components/ui';
import { insightsApi } from '../../api/insights';
import { useAsync } from '../../hooks/useAsync';
import i18n from '../../i18n';
import { intlLocale } from '../../i18n/core';
import { formatCents } from '../../lib/money';
import type { ProductInsight, ProductInsightKind } from '../../types/domain';

const kindBadgeClass: Record<ProductInsightKind, string> = {
  shrinkflation: 'badge badge-insight-watch',
  bulk_buy: 'badge badge-insight-opportunity',
  price_creep: 'badge badge-insight-watch',
  category_spike: 'badge badge-insight-watch',
};

/** The dashboard's unread insights: one row per saved nudge with a dismiss
 *  button. Hidden entirely when nothing is unread. */
export function InsightsCard() {
  const { t } = useTranslation('dashboard');
  const unseen = useAsync(() => insightsApi.list({ unseen: true, limit: 10 }), []);
  // Rows the user just dismissed — locally removed immediately even if the
  // refetch is still in flight (a failed dismiss restores the row).
  const [pendingDismissed, setPendingDismissed] = useState<Set<number>>(new Set());

  /** "2026-10" → "Oct 2026" (a malformed label stays as it came). */
  const monthLabel = (month: string): string => {
    const m = /^(\d{4})-(\d{2})$/.exec(month);
    if (!m) return month;
    const date = new Date(Number(m[1]), Number(m[2]) - 1, 1);
    return date.toLocaleString(intlLocale(i18n.language), { month: 'short', year: 'numeric' });
  };

  /** Relative age of a unix-second timestamp ("just now", "3d ago"). */
  const ageOf = (unixSeconds: number): string => {
    const days = Math.floor((Date.now() / 1000 - unixSeconds) / 86400);
    if (days <= 0) return t('insights.relative.justNow');
    if (days === 1) return t('insights.relative.yesterday');
    if (days < 30) return t('insights.relative.daysAgo', { count: days });
    const months = Math.floor(days / 30);
    return t('insights.relative.monthsAgo', { count: months });
  };

  /** The facts line under the nudge: what changed, in the saved numbers. */
  const factsOf = (insight: ProductInsight): string => {
    const d = insight.data;
    const parts: string[] = [];
    if (d.month !== undefined && d.current_cents !== undefined && d.baseline_cents !== undefined) {
      parts.push(
        t('facts.avgCompare', {
          month: monthLabel(d.month),
          current: formatCents(d.current_cents, insight.currency),
          baseline: formatCents(d.baseline_cents, insight.currency),
        }),
      );
    }
    if (d.old_price_cents !== undefined && d.new_price_cents !== undefined) {
      parts.push(
        t('facts.unitPrice', {
          old: formatCents(d.old_price_cents, insight.currency),
          new: formatCents(d.new_price_cents, insight.currency),
        }),
      );
    }
    if (d.old_unit_value !== undefined && d.new_unit_value !== undefined) {
      parts.push(t('facts.size', { old: d.old_unit_value, new: d.new_unit_value, unit: d.unit }));
    } else if (d.old_ppu !== undefined && d.new_ppu !== undefined) {
      parts.push(
        t('facts.ppu', {
          unit: d.unit,
          old: formatCents(Math.round(d.old_ppu), insight.currency),
          new: formatCents(Math.round(d.new_ppu), insight.currency),
        }),
      );
    }
    return parts.join(' · ');
  };

  if (unseen.loading) {
    return (
      <Card title={t('insights.title')} className="insights-card">
        <p className="hint-text">{t('insights.loading')}</p>
      </Card>
    );
  }
  if (unseen.error) {
    return (
      <Card title={t('insights.title')} className="insights-card">
        <ErrorMessage message={unseen.error.message} />
      </Card>
    );
  }
  const rows = (unseen.data ?? []).filter((r) => !pendingDismissed.has(r.id));
  if (rows.length === 0) return null;

  const dismiss = async (id: number) => {
    setPendingDismissed((prev) => new Set(prev).add(id));
    try {
      await insightsApi.dismiss(id);
    } catch {
      setPendingDismissed((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      unseen.reload();
    }
  };

  return (
    <Card title={t('insights.title')} className="insights-card">
      <div className="insight-list">
        {rows.map((insight) => (
          <div key={insight.id} className="insight-row">
            <span className={kindBadgeClass[insight.kind]}>{t(`insights.kind.${insight.kind}`)}</span>
            <div className="insight-body">
              <span className="insight-message">
                <strong>{insight.product_name || insight.category_name}</strong> — {insight.message}
              </span>
              {factsOf(insight) && (
                <span className="hint-inline">{factsOf(insight)}</span>
              )}
            </div>
            <span className="insight-age hint-inline">{ageOf(Number(insight.created_at))}</span>
            <Button variant="ghost" onClick={() => void dismiss(insight.id)}>
              {t('insights.dismiss')}
            </Button>
          </div>
        ))}
      </div>
    </Card>
  );
}