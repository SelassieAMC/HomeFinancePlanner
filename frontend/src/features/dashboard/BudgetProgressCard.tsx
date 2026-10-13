import { useAsync } from '../../hooks/useAsync';
import type { AsyncState } from '../../hooks/useAsync';
import { categoriesApi } from '../../api/categories';
import { Card, ErrorMessage } from '../../components/ui';
import { formatCents } from '../../lib/money';
import { todayISO } from '../../lib/date';
import type { BudgetStatus, Summary } from '../../types/domain';

/** Short month label for the section title / a monthly row ("2026-10" → "Oct 2026"). */
function monthTag(month: string): string {
  return new Date(`${month}-01T00:00:00`).toLocaleDateString('en-US', {
    month: 'short',
    year: 'numeric',
  });
}

/** Number of days in a YYYY-MM month. */
function daysInMonth(month: string): number {
  const [year, monthIdx] = month.split('-').map(Number);
  return new Date(year ?? 2000, monthIdx ?? 12, 0).getDate();
}

/**
 * Roadmap Step 2 on the dashboard: the displayed month's monthly-budget caps
 * plus the still-open lifetime envelopes, reusing the month summary the stat
 * tiles already fetch (`Summary.budgets`) — no extra aggregate endpoint.
 *
 * Monthly rows bound their spend to their own month (migration 0032), so a
 * monthly row shows up exactly when the displayed month is the row's month;
 * envelopes show lifetime progress, like the Budgets page. For capped rows in
 * the *current* calendar month a pace line projects when the cap would run
 * out if the month's elapsed pace continues. Hidden entirely when nothing
 * qualifies.
 */
export function BudgetProgressCard({ summary }: { summary: AsyncState<Summary> }) {
  const categories = useAsync(() => categoriesApi.list(), []);

  if (summary.loading) {
    return (
      <Card title="Budgets" className="budgets-card">
        <p className="hint-text">Loading budgets…</p>
      </Card>
    );
  }
  if (summary.error || !summary.data) {
    return (
      <Card title="Budgets" className="budgets-card">
        <ErrorMessage message={summary.error?.message ?? 'Summary unavailable.'} />
      </Card>
    );
  }

  const month = summary.data.from.slice(0, 7);
  const currency = summary.data.currency;
  const catById = new Map((categories.data ?? []).map((c) => [c.id, c]));

  // Monthly rows for the displayed month (closed ones the summary kept for
  // in-range activity stay visible as the month's history) and every open
  // envelope; finished envelopes are the Budgets page's concern.
  const rows = (summary.data.budgets ?? []).filter((b) =>
    b.cadence === 'monthly' ? b.month === month : b.status === 'open',
  );
  if (rows.length === 0) return null;

  // Pace projection reference point: the day inside the displayed month.
  // Projection only makes sense for the current calendar month — history has
  // no velocity left to predict.
  const today = todayISO();
  const currentMonth = today.slice(0, 7);
  const elapsedDays = Number(today.slice(8, 10)) || 1;
  const monthDays = daysInMonth(month);

  const rowOrder = (r: BudgetStatus) => (r.remaining_cents < 0 ? 0 : 1); // overspent first
  const cadenceOrder = (r: BudgetStatus) => (r.cadence === 'monthly' ? 0 : 1);
  rows.sort(
    (a, b) =>
      rowOrder(a) - rowOrder(b) ||
      cadenceOrder(a) - cadenceOrder(b) ||
      (catById.get(a.category_id)?.name ?? '').localeCompare(catById.get(b.category_id)?.name ?? ''),
  );

  return (
    <Card title={`Budgets · ${monthTag(month)}`} className="budgets-card">
      <div className="budget-list">
        {rows.map((b) => {
          const cat = catById.get(b.category_id);
          const isClosed = b.status === 'closed';
          const over = b.remaining_cents < 0;
          const pct = b.amount_cents > 0 ? (b.lifetime_spent_cents / b.amount_cents) * 100 : 0;

          // Only for capped rows in the current month, mid-month, still under
          // the cap: would the month's pace so far exhaust the remaining
          // budget before the month ends?
          let paceWarning = '';
          if (b.cadence === 'monthly' && b.month === currentMonth && !isClosed && !over && elapsedDays >= 2) {
            const pace = b.spent_cents / elapsedDays;
            const daysToExhaust = pace > 0 ? b.remaining_cents / pace : Infinity;
            if (daysToExhaust < monthDays - elapsedDays) {
              paceWarning = `At this pace you'd exceed the cap in ${Math.max(1, Math.floor(daysToExhaust))} days`;
            }
          }

          return (
            <Card className="budget-card" key={b.id}>
              <div className="budget-head">
                <span
                  className={
                    isClosed ? 'budget-dot closed' : over ? 'budget-dot over' : 'budget-dot'
                  }
                  aria-hidden="true"
                />
                <span className="budget-name">
                  {cat?.icon && <span aria-hidden="true">{cat.icon} </span>}
                  {cat?.name ?? `#${b.category_id}`}
                </span>
                {b.cadence === 'monthly' && (
                  <span className="stat-label">
                    Monthly{b.month ? ` · ${monthTag(b.month)}` : ''}
                  </span>
                )}
                <span className="budget-amounts">
                  <strong>{formatCents(b.lifetime_spent_cents, currency)}</strong>
                  {' / '}
                  {formatCents(b.amount_cents, currency)}
                </span>
              </div>
              <div className="progress-track">
                <div
                  className={over ? 'progress-fill over' : 'progress-fill'}
                  style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
                />
              </div>
              <div
                className={
                  isClosed ? 'budget-status' : over ? 'budget-status over' : 'budget-status ok'
                }
              >
                {isClosed
                  ? `Closed · spent ${formatCents(b.lifetime_spent_cents, currency)} of ${formatCents(
                      b.amount_cents,
                      currency,
                    )}`
                  : over
                    ? `Over by ${formatCents(-b.remaining_cents, currency)}`
                    : `${formatCents(b.remaining_cents, currency)} left`}
              </div>
              {paceWarning && <div className="hint-inline">{paceWarning}</div>}
              {/* Envelopes get lifetime progress; the in-range figure shows
                  what this month alone added to the pool. */}
              {b.cadence === 'open' && b.spent_cents > 0 && (
                <div className="hint-inline">{formatCents(b.spent_cents, currency)} in {monthTag(month)}</div>
              )}
            </Card>
          );
        })}
      </div>
    </Card>
  );
}