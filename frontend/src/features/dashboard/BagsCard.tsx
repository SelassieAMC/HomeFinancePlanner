import { useTranslation } from 'react-i18next';
import type { AsyncState } from '../../hooks/useAsync';
import { Card } from '../../components/ui';
import { formatCents } from '../../lib/money';
import type { BagOverview, BagStatus } from '../../types/domain';

/** Do any accounts carry bags? Shared by the tabbed planning card's emptiness
 *  check and tab defaulting. */
export function bagsHaveContent(overview: BagOverview | null): boolean {
  return (overview?.accounts ?? []).some((v) => v.bags.length > 0);
}

/**
 * Body of the dashboard bags section (no Card wrapper — the tabbed planning
 * card wraps it). The caller owns the fetch and the empty state; this renders
 * open reserves with their computed balance and target progress, plus each
 * account's oversubscription warning. Balances are derived per read
 * (Σ allocations − tagged spend); accounts without bags are dropped.
 */
export function BagsBody({ overview }: { overview: AsyncState<BagOverview | null> }) {
  const { t } = useTranslation('dashboard');
  if (!overview.data) return null;

  const views = overview.data.accounts.filter((v) => v.bags.length > 0);
  if (views.length === 0) return null;

  return (
    <div className="budget-list">
      {views.map((v) => {
        const open: BagStatus[] = v.bags.filter((b) => b.status === 'open');
        return (
          <div className="bag-section" key={v.account_id}>
            {open.map((b) => {
              const pct = b.target_cents != null && b.target_cents > 0 ? (b.balance_cents / b.target_cents) * 100 : 0;
              const ok = b.target_cents == null ? b.balance_cents >= 0 : b.balance_cents >= (b.target_cents ?? 0);
              return (
                <Card className="budget-card" key={b.id}>
                  <div className="budget-head">
                    <span className={ok ? 'budget-dot ok' : 'budget-dot'} aria-hidden="true" />
                    <span className="budget-name">{b.name}</span>
                    <span className="stat-label">{b.account_name}</span>
                    <span className="budget-amounts">
                      <strong>{formatCents(b.balance_cents, v.currency)}</strong>
                      {b.target_cents != null && (
                        <span>{' / '}{formatCents(b.target_cents, v.currency)}</span>
                      )}
                    </span>
                  </div>
                  {b.target_cents != null && b.target_cents > 0 && (
                    <div className="progress-track">
                      <div
                        className={ok ? 'progress-fill done' : 'progress-fill'}
                        style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
                      />
                    </div>
                  )}
                  <div className="budget-status">
                    {t('bags.spentUnbagged', {
                      spent: formatCents(b.tagged_spend_cents, v.currency),
                      unbagged: formatCents(v.unbagged_cents, v.currency),
                    })}
                  </div>
                  {v.oversubscribed && (
                    <div className="budget-status over">{t('bags.oversubscribed')}</div>
                  )}
                </Card>
              );
            })}
          </div>
        );
      })}
    </div>
  );
}