import { useAsync } from '../../hooks/useAsync';
import { goalsApi } from '../../api/goals';
import { Card } from '../../components/ui';
import { formatCents } from '../../lib/money';
import type { GoalStatus } from '../../types/domain';

/**
 * Roadmap gap 4 on the dashboard: savings goals with their live progress
 * (the tracked account's balance; contributions are transfers into it).
 * Open goals sort first, achieved ones trail. Hidden entirely when there are
 * no goals — like the budget card, an empty section is noise.
 */
export function GoalsCard() {
  const goals = useAsync(() => goalsApi.list(), []);

  if (goals.loading) return null;
  if (goals.error) return null;
  const rows: GoalStatus[] = [...(goals.data ?? [])].sort(
    (a, b) => (a.achieved ? 1 : 0) - (b.achieved ? 1 : 0) || a.name.localeCompare(b.name),
  );
  if (rows.length === 0) return null;

  return (
    <Card title="Goals" className="budgets-card">
      <div className="budget-list">
        {rows.map((g) => {
          const pct = g.target_cents > 0 ? (g.progress_cents / g.target_cents) * 100 : 0;
          const remaining = Math.max(0, g.target_cents - g.progress_cents);
          return (
            <Card className="budget-card" key={g.id}>
              <div className="budget-head">
                <span className={g.achieved ? 'budget-dot ok' : 'budget-dot'} aria-hidden="true" />
                <span className="budget-name">{g.name}</span>
                <span className="stat-label">{g.account_name}</span>
                <span className="budget-amounts">
                  <strong>{formatCents(g.progress_cents, g.currency)}</strong>
                  {' / '}
                  {formatCents(g.target_cents, g.currency)}
                </span>
              </div>
              <div className="progress-track">
                <div
                  className={g.achieved ? 'progress-fill done' : 'progress-fill'}
                  style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
                />
              </div>
              <div className={g.achieved ? 'budget-status ok' : 'budget-status'}>
                {g.achieved
                  ? 'Goal reached'
                  : `${formatCents(remaining, g.currency)} to go${g.target_date ? ` · by ${g.target_date}` : ''}`}
              </div>
            </Card>
          );
        })}
      </div>
    </Card>
  );
}