import { useAsync } from '../../hooks/useAsync';
import type { AsyncState } from '../../hooks/useAsync';
import { categoriesApi } from '../../api/categories';
import { Card } from '../../components/ui';
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

/** The dashboard budget section's qualifying rows: the displayed month's
 *  monthly caps (closed in-range rows included as the month's history) plus
 *  every still-open envelope. Shared by the body and the tabbed planning
 *  card's emptiness check. */
export function budgetRowsFor(data: Summary): BudgetStatus[] {
  const month = data.from.slice(0, 7);
  return (data.budgets ?? []).filter((b) =>
    b.cadence === 'monthly' ? b.month === month : b.status === 'open',
  );
}

/** Body of the budgets section (no Card wrapper — the tabbed planning card
 *  and a standalone card shell wrap it). Assumes the summary loaded; null
 *  when nothing qualifies. */
export function BudgetsBody({ summary }: { summary: AsyncState<Summary> }) {
  const categories = useAsync(() => categoriesApi.list(), []);

  const data = summary.data;
  if (!data) return null;

  const month = data.from.slice(0, 7);
  const currency = data.currency;
  const catById = new Map((categories.data ?? []).map((c) => [c.id, c]));

  // Monthly rows for the displayed month (closed ones the summary kept for
  // in-range activity stay visible as the month's history) and every open
  // envelope; finished envelopes are the Budgets page's concern.
  const rows = budgetRowsFor(data);
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
  );
}