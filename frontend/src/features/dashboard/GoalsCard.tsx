import { useTranslation } from 'react-i18next';
import type { AsyncState } from '../../hooks/useAsync';
import { formatCents } from '../../lib/money';
import type { GoalStatus } from '../../types/domain';
import { PlanRow } from './PlanRow';

/**
 * Body of the dashboard goals section (no Card wrapper — the tabbed planning
 * card wraps it). The caller owns the fetch and the empty state; this renders
 * the list only: per-goal progress cards (open first, achieved trailing).
 * Progress is the tracked account's live balance — contributions are
 * transfers into it.
 */
export function GoalsBody({ goals }: { goals: AsyncState<GoalStatus[]> }) {
  const { t } = useTranslation('dashboard');
  if (!goals.data) return null;
  const rows: GoalStatus[] = [...goals.data].sort(
    (a, b) => (a.achieved ? 1 : 0) - (b.achieved ? 1 : 0) || a.name.localeCompare(b.name),
  );
  if (rows.length === 0) return null;

  return (
    <div className="plan-list">
      {rows.map((g) => {
        const pct = g.target_cents > 0 ? (g.progress_cents / g.target_cents) * 100 : 0;
        const remaining = Math.max(0, g.target_cents - g.progress_cents);
        return (
          <PlanRow
            key={g.id}
            tone={g.achieved ? 'ok' : 'normal'}
            name={g.name}
            context={g.account_name}
            primary={formatCents(g.progress_cents, g.currency)}
            target={formatCents(g.target_cents, g.currency)}
            pct={pct}
            status={
              g.achieved
                ? t('goals.reached')
                : g.target_date
                  ? t('goals.toGoBy', { amount: formatCents(remaining, g.currency), date: g.target_date })
                  : t('goals.toGo', { amount: formatCents(remaining, g.currency) })
            }
            statusTone={g.achieved ? 'ok' : 'normal'}
          />
        );
      })}
    </div>
  );
}