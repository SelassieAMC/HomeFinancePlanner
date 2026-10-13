import type { AsyncState } from '../../hooks/useAsync';
import type { Glance } from '../../types/domain';
import { formatCents, formatSignedCents } from '../../lib/money';
import { Card } from '../../components/ui';

function monthLabel(month: string): string {
  return new Date(`${month}-01T00:00:00`).toLocaleDateString('en-US', { month: 'long' });
}

/**
 * Step-1 "at a glance" row: current financial standing, independent of the
 * period the dashboard below is navigated to. The fetch happens in
 * DashboardPage so its conversion warnings join the page-wide banner.
 */
export function AtAGlanceRow({ glance }: { glance: AsyncState<Glance> }) {
  const d = glance.data;
  const cur = d?.currency ?? '';

  if (glance.error) {
    return <div className="bill-warning">At a glance unavailable: {glance.error.message}</div>;
  }
  if (glance.loading || !d) {
    return (
      <div className="stat-grid glance">
        {['Net Worth', 'Cash Flow', 'Burn Rate', 'Receipts to review'].map((label) => (
          <Card className="stat-tile" key={label}>
            <span className="stat-label">{label}</span>
            <span className="stat-value">…</span>
          </Card>
        ))}
      </div>
    );
  }

  const netTone = d.net_cents >= 0 ? 'stat-positive' : 'stat-negative';
  return (
    <div className="stat-grid glance">
      <Card className="stat-tile">
        <span className="stat-label">Net worth, all accounts</span>
        <span className="stat-value">{formatCents(d.net_worth_cents, cur)}</span>
        <span className="stat-label">today</span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">Cash flow in {monthLabel(d.month)}</span>
        <span className={`stat-value ${netTone}`}>{formatSignedCents(d.net_cents, cur)}</span>
        <span className="stat-label">
          {formatCents(d.income_cents, cur)} in · {formatCents(d.expense_cents, cur)} out
        </span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">Burn rate</span>
        <span className="stat-value">{formatCents(d.burn_rate_cents, cur)}</span>
        <span className="stat-label">avg. spent per day, last 30 days</span>
      </Card>
      <Card className="stat-tile">
        <span className="stat-label">Receipts to review</span>
        <span className="stat-value">{d.pending_receipts}</span>
        <span className="stat-label">
          {d.pending_receipts === 0
            ? 'nothing waiting'
            : `${formatCents(d.pending_receipt_cents, cur)} awaiting review`}
        </span>
      </Card>
    </div>
  );
}
