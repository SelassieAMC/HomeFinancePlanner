import { useMemo, useState } from 'react';
import { Button, Card, ErrorMessage } from '../../components/ui';
import { schedulesApi } from '../../api/schedules';
import { useAsync } from '../../hooks/useAsync';
import { todayISO } from '../../lib/date';
import type { ScheduleKind, ScheduleRunView } from '../../types/domain';
import { Link } from 'react-router-dom';

// The dashboard's "Coming up": the scheduler's pending occurrences due today,
// overdue or within the next 7 days — the confirm-first nudge (apply with one
// click, dismiss, skip) plus the virtual ones that applied themselves. Hidden
// entirely when nothing is due, like the insights card.

const VIRTUAL_KINDS: readonly ScheduleKind[] = ['bag_funding', 'bag_rebalance'];

/** Everything due within [overdue … today + 7 days], due-ascending. */
function dueWindow(today: string): string {
  const limit = new Date(`${today}T00:00:00`);
  limit.setDate(limit.getDate() + 7);
  return `${limit.getFullYear()}-${String(limit.getMonth() + 1).padStart(2, '0')}-${String(limit.getDate()).padStart(2, '0')}`;
}

function dueLabel(due: string, today: string): string {
  if (due < today) return 'Overdue';
  if (due === today) return 'Today';
  const days = Math.round(
    (new Date(`${due}T00:00:00`).getTime() - new Date(`${today}T00:00:00`).getTime()) / 86400000,
  );
  return days === 1 ? 'Tomorrow' : `In ${days} days`;
}

export function ComingUpCard() {
  const today = todayISO();
  const runs = useAsync(() => schedulesApi.runs({ status: ['pending'], due_to: dueWindow(today), limit: 50 }), []);
  const [busy, setBusy] = useState(false);

  const rows = useMemo(() => {
    const all = runs.data ?? [];
    return all.filter((r) => r.due_date < dueWindow(today)).sort((a, b) => a.due_date.localeCompare(b.due_date));
  }, [runs.data, today]);

  async function act(run: ScheduleRunView, action: 'apply' | 'dismiss' | 'skip') {
    setBusy(true);
    try {
      if (action === 'apply') await schedulesApi.applyRun(run.id);
      else if (action === 'dismiss') await schedulesApi.dismissRun(run.id);
      else await schedulesApi.skipRun(run.id);
      await runs.reload();
    } finally {
      setBusy(false);
    }
  }

  if (runs.loading) {
    return (
      <Card title="Coming up" className="insights-card">
        <p className="hint-text">Checking the planner…</p>
      </Card>
    );
  }
  if (runs.error) {
    return (
      <Card title="Coming up" className="insights-card">
        <ErrorMessage message={runs.error.message} />
      </Card>
    );
  }
  if (rows.length === 0) return null;

  const overduePresent = rows.some((r) => r.due_date < today);

  return (
    <Card title="Coming up" className="insights-card">
      <div className="insight-list">
        {rows.map((run) => (
          <div key={run.id} className="insight-row">
            <span className={run.due_date < today ? 'badge danger' : 'badge'}>{dueLabel(run.due_date, today)}</span>
            <div className="insight-body">
              <span className="insight-message">
                <strong>{run.action_name}</strong>
                {' — '}
                {run.kind === 'purchase'
                  ? 'checklist item'
                  : run.kind === 'account_transfer'
                    ? 'account transfer'
                    : run.kind === 'transaction'
                      ? (run.schedule.transaction_kind ?? 'transaction')
                      : run.kind === 'bag_funding'
                        ? 'bag funding'
                        : 'bag rebalance'}
              </span>
              <span className="hint-inline">Planned via Scheduled · Planning</span>
            </div>
            <div className="budget-actions">
              <Button
                variant="secondary"
                className="btn-sm"
                disabled={busy}
                onClick={() => void act(run, 'apply')}
              >
                {run.kind === 'purchase' ? 'Done' : 'Apply'}
              </Button>
              {!VIRTUAL_KINDS.includes(run.kind) && (
                <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => void act(run, 'dismiss')}>
                  Dismiss
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      <p className="hint-text">
        {overduePresent ? 'Overdue items nudge until you deal with them. ' : ''}
        Full list on the <Link to="/planning?tab=schedules">Scheduled</Link> planning page.
      </p>
    </Card>
  );
}