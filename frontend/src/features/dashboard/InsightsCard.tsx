import { useState } from 'react';
import { Button, Card, ErrorMessage } from '../../components/ui';
import { insightsApi } from '../../api/insights';
import { useAsync } from '../../hooks/useAsync';
import { formatCents } from '../../lib/money';
import type { ProductInsight, ProductInsightKind } from '../../types/domain';

// Kind → badge text. The badge class encodes the reading: green for
// opportunities (bulk_buy), red for watches (shrinkflation, price_creep).
const kindLabel: Record<ProductInsightKind, string> = {
  shrinkflation: 'Shrinkflation',
  bulk_buy: 'Bulk opportunity',
  price_creep: 'Price creep',
};

const kindBadgeClass: Record<ProductInsightKind, string> = {
  shrinkflation: 'badge badge-insight-watch',
  bulk_buy: 'badge badge-insight-opportunity',
  price_creep: 'badge badge-insight-watch',
};

/** Relative age of a unix-second timestamp ("just now", "3d ago"). */
function relativeAge(unixSeconds: number): string {
  const days = Math.floor((Date.now() / 1000 - unixSeconds) / 86400);
  if (days <= 0) return 'just now';
  if (days === 1) return 'yesterday';
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return months === 1 ? '1 month ago' : `${months} months ago`;
}

/** The facts line under the nudge: what changed, in the saved numbers. */
function factsOf(insight: ProductInsight): string {
  const d = insight.data;
  const parts: string[] = [];
  if (d.old_price_cents !== undefined && d.new_price_cents !== undefined) {
    parts.push(`unit price ${formatCents(d.old_price_cents, insight.currency)} → ${formatCents(d.new_price_cents, insight.currency)}`);
  }
  if (d.old_unit_value !== undefined && d.new_unit_value !== undefined) {
    parts.push(`size ${d.old_unit_value} ${d.unit} → ${d.new_unit_value} ${d.unit}`);
  } else if (d.old_ppu !== undefined && d.new_ppu !== undefined) {
    parts.push(`per ${d.unit} ${formatCents(Math.round(d.old_ppu), insight.currency)} → ${formatCents(Math.round(d.new_ppu), insight.currency)}`);
  }
  return parts.join(' · ');
}

/** The dashboard's unread insights: one row per saved nudge with a dismiss
 *  button. Hidden entirely when nothing is unread. */
export function InsightsCard() {
  const unseen = useAsync(() => insightsApi.list({ unseen: true, limit: 10 }), []);
  // Rows the user just dismissed — locally removed immediately even if the
  // refetch is still in flight (a failed dismiss restores the row).
  const [pendingDismissed, setPendingDismissed] = useState<Set<number>>(new Set());

  if (unseen.loading) {
    return (
      <Card title="Purchase insights" className="insights-card">
        <p className="hint-text">Loading insights…</p>
      </Card>
    );
  }
  if (unseen.error) {
    return (
      <Card title="Purchase insights" className="insights-card">
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
    <Card title="Purchase insights" className="insights-card">
      <div className="insight-list">
        {rows.map((insight) => (
          <div key={insight.id} className="insight-row">
            <span className={kindBadgeClass[insight.kind]}>{kindLabel[insight.kind]}</span>
            <div className="insight-body">
              <span className="insight-message">
                <strong>{insight.product_name}</strong> — {insight.message}
              </span>
              {factsOf(insight) && (
                <span className="hint-inline">{factsOf(insight)}</span>
              )}
            </div>
            <span className="insight-age hint-inline">{relativeAge(Number(insight.created_at))}</span>
            <Button variant="ghost" onClick={() => void dismiss(insight.id)}>
              Dismiss
            </Button>
          </div>
        ))}
      </div>
    </Card>
  );
}