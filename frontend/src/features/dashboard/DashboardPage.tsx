import { useAsync } from '../../hooks/useAsync';
import { summaryApi } from '../../api/summary';
import { billsApi } from '../../api/bills';
import { categoriesApi } from '../../api/categories';
import { formatCents, formatSignedCents, currentMonth } from '../../lib/money';
import { Card, Spinner, ErrorMessage, EmptyState } from '../../components/ui';
import { ExpensesByDayChart } from './ExpensesByDayChart';

export function DashboardPage() {
  const month = currentMonth();
  const { data, loading, error } = useAsync(() => summaryApi.month(month), [month]);
  // Global per-product-category spending from accepted bill lines.
  const productStats = useAsync(() => billsApi.stats('category', month), [month]);
  const categories = useAsync(() => categoriesApi.list(), []);

  if (loading) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;
  if (!data) return <EmptyState message="No summary data yet." />;

  const hasBudgets = data.budgets.length > 0;
  const hasTopCategories = data.top_categories.length > 0;
  const productRows = productStats.data?.rows ?? [];
  const productCurrency = productStats.data?.currency ?? data.currency;
  const categoryInfo = new Map((categories.data ?? []).map((c) => [c.name, c]));
  // Currencies with no exchange rate are shown 1:1 — surface that explicitly.
  const conversionWarning = data.conversion_warnings.length > 0
    ? `Exchange rate missing for ${data.conversion_warnings.join(', ')} — those amounts are shown 1:1.`
    : '';

  return (
    <div className="page">
      <h2 className="page-title">Dashboard — {month}</h2>

      {conversionWarning && <div className="bill-warning">{conversionWarning}</div>}

      <div className="stat-grid">
        <Card className="stat-tile">
          <span className="stat-label">Income</span>
          <span className="stat-value stat-positive">{formatCents(data.income_cents, data.currency)}</span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">Expenses</span>
          <span className="stat-value stat-negative">{formatCents(data.expense_cents, data.currency)}</span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">Net</span>
          <span className="stat-value">{formatSignedCents(data.net_cents, data.currency)}</span>
        </Card>
        <Card className="stat-tile">
          <span className="stat-label">Total balance</span>
          <span className="stat-value">{formatCents(data.total_balance_cents, data.currency)}</span>
        </Card>
      </div>

      <Card title="Expenses by day">
        {data.daily_expenses.length === 0 ? (
          <EmptyState message="No expenses recorded this month yet." />
        ) : (
          <ExpensesByDayChart days={data.daily_expenses} month={month} currency={data.currency} />
        )}
      </Card>

      <Card title="Spending by product category">
        {productStats.loading ? (
          <Spinner />
        ) : productStats.error ? (
          <ErrorMessage message={productStats.error.message} />
        ) : productRows.length === 0 ? (
          <EmptyState message="No accepted bills this month yet." />
        ) : (
          <ul className="simple-list">
            {productRows.map((row) => {
              const cat = categoryInfo.get(row.label);
              return (
                <li key={row.label}>
                  <span>
                    {cat?.icon ? `${cat.icon} ` : ''}
                    {row.label}
                  </span>
                  <span>{formatCents(row.total_cents, productCurrency)}</span>
                </li>
              );
            })}
          </ul>
        )}
      </Card>

      <div className="two-col">
        <Card title="Budget progress">
          {!hasBudgets ? (
            <EmptyState message="No budgets set for this month." />
          ) : (
            <ul className="budget-list">
              {data.budgets.map((b) => {
                const cat = (categories.data ?? []).find((c) => c.id === b.category_id);
                return (
                  <li key={b.id} className="budget-item">
                    <div className="budget-row">
                      <span>
                        {cat?.icon ? `${cat.icon} ` : ''}
                        {cat?.name ?? `Category #${b.category_id}`}
                      </span>
                      <span>
                        {formatCents(b.spent_cents, data.currency)} / {formatCents(b.amount_cents, data.currency)}
                      </span>
                    </div>
                    <div className="progress-track">
                      <div
                        className={b.remaining_cents < 0 ? 'progress-fill over' : 'progress-fill'}
                        style={{ width: `${Math.min(100, (b.spent_cents / b.amount_cents) * 100)}%` }}
                      />
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>

        <Card title="Top spending categories">
          {!hasTopCategories ? (
            <EmptyState message="No spending recorded this month." />
          ) : (
            <ul className="simple-list">
              {data.top_categories.map((c) => (
                <li key={c.category_id}>
                  <span>{c.category_name || `Category #${c.category_id}`}</span>
                  <span>{formatCents(c.total_cents, data.currency)}</span>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}