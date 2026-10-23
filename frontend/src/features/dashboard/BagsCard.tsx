import { useTranslation } from 'react-i18next';
import type { AsyncState } from '../../hooks/useAsync';
import { formatCents } from '../../lib/money';
import type { BagOverview, BagStatus } from '../../types/domain';
import { PlanAccountHead, PlanRow } from './PlanRow';

/** Do any accounts carry bags? Shared by the tabbed planning card's emptiness
 *  check and tab defaulting. */
export function bagsHaveContent(overview: BagOverview | null): boolean {
  return (overview?.accounts ?? []).some((v) => v.bags.length > 0);
}

/**
 * Body of the dashboard bags section (no Card wrapper — the tabbed planning
 * card wraps it). The caller owns the fetch and the empty state. Bags render
 * as compact rows grouped under one header per account — the account's
 * balance and unbagged remainder (plus the oversubscription flag) belong to
 * the account, so they appear once in the header instead of in every bag.
 * Balances are derived per read (Σ allocations − tagged spend); accounts
 * without bags are dropped, and closed bags stay the Planning page's concern.
 */
export function BagsBody({ overview }: { overview: AsyncState<BagOverview | null> }) {
  const { t } = useTranslation('dashboard');
  if (!overview.data) return null;

  const views = overview.data.accounts.filter((v) => v.bags.length > 0);
  if (views.length === 0) return null;

  return (
    <div className="plan-list">
      {views.map((v) => {
        const open: BagStatus[] = v.bags.filter((b) => b.status === 'open');
        return (
          <div className="plan-account" key={v.account_id}>
            <PlanAccountHead
              name={v.account_name}
              balance={formatCents(v.account_balance_cents, v.currency)}
              unbagged={t('bags.unbagged', { unbagged: formatCents(v.unbagged_cents, v.currency) })}
              warn={v.oversubscribed ? t('bags.oversubscribed') : undefined}
            />
            {open.map((b) => {
              const pct = b.target_cents != null && b.target_cents > 0 ? (b.balance_cents / b.target_cents) * 100 : 0;
              const ok = b.target_cents == null ? b.balance_cents >= 0 : b.balance_cents >= (b.target_cents ?? 0);
              return (
                <PlanRow
                  key={b.id}
                  tone={ok ? 'ok' : 'normal'}
                  name={b.name}
                  primary={formatCents(b.balance_cents, v.currency)}
                  target={b.target_cents != null ? formatCents(b.target_cents, v.currency) : undefined}
                  pct={b.target_cents != null && b.target_cents > 0 ? pct : null}
                  status={t('bags.spent', { spent: formatCents(b.tagged_spend_cents, v.currency) })}
                />
              );
            })}
          </div>
        );
      })}
    </div>
  );
}