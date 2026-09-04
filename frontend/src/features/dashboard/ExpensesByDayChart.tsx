import type { DayTotal } from '../../types/domain';
import { formatCents } from '../../lib/money';

// ExpensesByDayChart renders one bar per day of the month using plain divs
// (responsive, no chart library). Days without expenses render as empty
// slots so the day axis stays aligned.
export function ExpensesByDayChart({ days, month }: { days: DayTotal[]; month: string }) {
  // Days in the month, "YYYY-MM-DD" → amount.
  const parts = month.split('-').map(Number);
  const year = parts[0] ?? new Date().getFullYear();
  const monthNum = parts[1] ?? new Date().getMonth() + 1;
  const daysInMonth = new Date(year, monthNum, 0).getDate();
  const byDate = new Map(days.map((d) => [d.date, d.expense_cents]));

  const bars = Array.from({ length: daysInMonth }, (_, i) => {
    const day = i + 1;
    const date = `${month}-${String(day).padStart(2, '0')}`;
    return { day, date, cents: byDate.get(date) ?? 0 };
  });

  const max = Math.max(...bars.map((b) => b.cents), 1);
  // Label roughly every 5th day depending on month length.
  const labelStep = daysInMonth > 20 ? 5 : 4;

  return (
    <div className="bar-chart" role="img" aria-label="Expenses by day">
      <div className="bar-chart-plot">
        {bars.map((b) => (
          <div className="bar-chart-col" key={b.day}>
            <div
              className={b.cents > 0 ? 'bar-chart-bar' : 'bar-chart-bar empty'}
              style={{ height: `${Math.max((b.cents / max) * 100, b.cents > 0 ? 4 : 0)}%` }}
              title={`${b.date}: ${formatCents(b.cents)}`}
            />
          </div>
        ))}
      </div>
      <div className="bar-chart-axis">
        {bars.map((b) => (
          <span key={b.day}>{b.day % labelStep === 1 ? b.day : ''}</span>
        ))}
      </div>
    </div>
  );
}