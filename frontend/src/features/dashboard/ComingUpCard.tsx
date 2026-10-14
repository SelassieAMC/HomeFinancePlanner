import { useMemo, useState } from 'react';
import { useTranslation, Trans } from 'react-i18next';
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

/** Days until the due date (past dates land on ≤ 0). */
function daysUntil(due: string, today: string): number {
  return Math.round(
    (new Date(`${due}T00:00:00`).getTime() - new Date(`${today}T00:00:00`).getTime()) / 86400000,
  );
}

export function ComingUpCard() {
  const { t } = useTranslation('dashboard');
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
      <Card title={t('comingUp.title')} className="insights-card">
        <p className="hint-text">{t('comingUp.loading')}</p>
      </Card>
    );
  }
  if (runs.error) {
    return (
      <Card title={t('comingUp.title')} className="insights-card">
        <ErrorMessage message={runs.error.message} />
      </Card>
    );
  }
  if (rows.length === 0) return null;

  const overduePresent = rows.some((r) => r.due_date < today);

  const dueLabel = (due: string): string => {
    if (due < today) return t('comingUp.due.overdue');
    if (due === today) return t('comingUp.due.today');
    const days = daysUntil(due, today);
    return days === 1 ? t('comingUp.due.tomorrow') : t('comingUp.due.inDays', { count: days });
  };

  return (
    <Card title={t('comingUp.title')} className="insights-card">
      <div className="insight-list">
        {rows.map((run) => (
          <div key={run.id} className="insight-row">
            <span className={run.due_date < today ? 'badge danger' : 'badge'}>{dueLabel(run.due_date)}</span>
            <div className="insight-body">
              <span className="insight-message">
                <strong>{run.action_name}</strong>
                {' — '}
                {run.kind === 'purchase'
                  ? t('comingUp.kind.purchase')
                  : run.kind === 'account_transfer'
                    ? t('comingUp.kind.transfer')
                    : run.kind === 'transaction'
                      ? (run.schedule.transaction_kind ?? t('comingUp.kind.transaction'))
                      : run.kind === 'bag_funding'
                        ? t('comingUp.kind.bag_funding')
                        : t('comingUp.kind.bag_rebalance')}
              </span>
              <span className="hint-inline">{t('comingUp.plannedHint')}</span>
            </div>
            <div className="budget-actions">
              <Button
                variant="secondary"
                className="btn-sm"
                disabled={busy}
                onClick={() => void act(run, 'apply')}
              >
                {run.kind === 'purchase' ? t('comingUp.done') : t('comingUp.apply')}
              </Button>
              {!VIRTUAL_KINDS.includes(run.kind) && (
                <Button variant="ghost" className="btn-sm" disabled={busy} onClick={() => void act(run, 'dismiss')}>
                  {t('comingUp.dismiss')}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      <p className="hint-text">
        {overduePresent ? `${t('comingUp.footerOverdue')} ` : ''}
        <Trans i18nKey="comingUp.footer">
          Full list on the <Link to="/planning?tab=schedules">Scheduled</Link> planning page.
        </Trans>
      </p>
    </Card>
  );
}