import { useMemo, useState } from 'react';
import type { Bill, Category } from '../../types/domain';
import { formatCents } from '../../lib/money';
import { EmptyState } from '../../components/ui';

// BillDraftView renders an accepted bill in the same mobile-first style as
// the scan draft: colored summary cards on top, one collapsible panel per
// article. Accepted bills are read-only — editing happens before confirm.
export function BillDraftView({ bill, categories = [] }: { bill: Bill; categories?: Category[] }) {
  const items = bill.items ?? [];
  const [search, setSearch] = useState('');
  const currency = bill.currency || 'USD';

  const query = search.trim().toLowerCase();
  const visibleItems = query
    ? items.filter((it) => `${it.name} ${it.brand ?? ''}`.toLowerCase().includes(query))
    : items;

  const mismatch = bill.printed_total_cents > 0 && bill.printed_total_cents !== bill.total_cents;
  const savings = items.reduce((sum, it) => sum + it.discount_cents, 0) + bill.discount_cents;

  // Product storage categories grouped by section for the read-only legend.
  const sections = useMemo(() => {
    const map = new Map<string, Category[]>();
    for (const c of categories) {
      if ((c.kind ?? 'product') !== 'product') continue;
      const key = c.section || 'Other';
      const list = map.get(key) ?? [];
      if (!map.has(key)) map.set(key, list);
      list.push(c);
    }
    return [...map.entries()];
  }, [categories]);

  function categoryFor(it: { category_id?: number | null; category_name?: string }) {
    return it.category_id ? categories.find((c) => c.id === it.category_id) : undefined;
  }

  return (
    <div className="bill-draft">
      <div className="stat-cards">
        <div className="stat-card stat-market">
          <span className="stat-card-label">Market</span>
          <span className="stat-card-value stat-card-text">{bill.market_name || '—'}</span>
          <span className="stat-card-sub">{bill.date || '—'}</span>
        </div>
        <div className="stat-card stat-total">
          <span className="stat-card-label">
            Total{' '}
            {mismatch && (
              <span
                className="warn-icon"
                title={`Printed on receipt: ${formatCents(bill.printed_total_cents, currency)} — does not match the calculated total`}
              >
                ⚠️
              </span>
            )}
          </span>
          <span className="stat-card-value">{formatCents(bill.total_cents, currency)}</span>
          <span className="stat-card-sub">
            {mismatch
              ? `Printed: ${formatCents(bill.printed_total_cents, currency)}`
              : 'VAT included'}
          </span>
        </div>
        <div className="stat-card stat-account">
          <span className="stat-card-label">Account</span>
          <span className="stat-card-value stat-card-text">
            {bill.account_name || '—'}
          </span>
          <span className="stat-card-sub">
            {bill.account_name
              ? bill.payment_method === 'cash'
                ? 'wallet money'
                : 'expense recorded here'
              : 'no account yet'}
          </span>
        </div>
        <div className="stat-card stat-account">
          <span className="stat-card-label">Payment</span>
          <span className="stat-card-value stat-card-text">
            {bill.payment_method || '—'}
            {bill.card_last_digits ? ` •${bill.card_last_digits}` : ''}
          </span>
          <span className="stat-card-sub">
            <a href={`/api/v1/bills/image/${bill.id}`} target="_blank" rel="noreferrer">
              View receipt
            </a>
          </span>
        </div>
        <div className="stat-card stat-savings">
          <span className="stat-card-label">Total savings</span>
          <span className="stat-card-value">{formatCents(savings, currency)}</span>
          <span className="stat-card-sub">all discounts</span>
        </div>
      </div>

      {mismatch && (
        <div className="bill-warning">
          ⚠️ The calculated total ({formatCents(bill.total_cents, currency)}) does not match the
          amount printed on the receipt ({formatCents(bill.printed_total_cents, currency)}).
        </div>
      )}

      {items.length > 1 && (
        <input
          className="search-input"
          type="search"
          placeholder="Search articles…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          aria-label="Filter articles"
        />
      )}

      {items.length === 0 ? (
        <EmptyState message="No articles were recorded for this bill." />
      ) : visibleItems.length === 0 ? (
        <EmptyState message={`No articles match “${search}”.`} />
      ) : (
        <div className="item-panels">
          {visibleItems.map((it) => {
            const cat = categoryFor(it);
            return (
              <details className="item-panel" key={it.id}>
                <summary>
                  <span
                    className="item-icon"
                    title={
                      it.is_return
                        ? 'Deposit return (money back)'
                        : cat?.name || it.category_name || 'Unclassified'
                    }
                  >
                    {it.is_return ? '♻️' : cat?.icon || '🛒'}
                  </span>
                  <span className="item-title">
                    <span className="item-name">{it.name}</span>
                    {it.is_return ? (
                      <span className="item-brand">♻️ deposit return</span>
                    ) : (
                      it.brand && <span className="item-brand">{it.brand}</span>
                    )}
                  </span>
                  <span className="item-price">{formatCents(it.line_total_cents, currency)}</span>
                </summary>
                <div className="item-detail">
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>Brand</span>
                      <span>{it.brand || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>Measure</span>
                      <span>{it.unit || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>Qty</span>
                      <span>{it.quantity}</span>
                    </div>
                  </div>
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>Unit price</span>
                      <span>{formatCents(it.unit_price_cents, currency)}</span>
                    </div>
                    <div className="item-field">
                      <span>Discount</span>
                      <span>
                        {it.discount_cents > 0
                          ? `−${formatCents(it.discount_cents, currency)}`
                          : '—'}
                      </span>
                    </div>
                    <div className="item-field">
                      <span>Line total</span>
                      <strong>{formatCents(it.line_total_cents, currency)}</strong>
                    </div>
                  </div>
                  <div className="item-field">
                    <span>Category</span>
                    <span>
                      {cat ? `${cat.icon} ${cat.name}` : it.category_name || 'Unclassified'}
                    </span>
                  </div>
                </div>
              </details>
            );
          })}
        </div>
      )}

      <div className="bill-totals">
        {bill.budget_name && (
          <div className="bill-total-line">
            <span>Budget</span>
            <span>{bill.budget_name}</span>
          </div>
        )}
        <div className="bill-total-line">
          <span>Items total ({items.length} articles)</span>
          <span>{formatCents(bill.items_subtotal_cents, currency)}</span>
        </div>
        <div className="bill-total-line">
          <span>Market discount (informational)</span>
          <span>{formatCents(bill.discount_cents, currency)}</span>
        </div>
        <div className="bill-total-line">
          <span>Total VAT/IVA (already in prices)</span>
          <span>{formatCents(bill.vat_cents, currency)}</span>
        </div>
        <div className="bill-total-line bill-total-row">
          <span>Total paid (VAT included)</span>
          <strong>{formatCents(bill.total_cents, currency)}</strong>
        </div>
      </div>

      {sections.length > 0 && (
        <details className="category-legend">
          <summary>Storage categories</summary>
          {sections.map(([section, cats]) => (
            <div key={section} className="legend-section">
              <span className="legend-title">{section}</span>
              <div className="legend-list">
                {cats.map((c) => (
                  <span key={c.id} className="legend-chip" title={c.description}>
                    {c.icon} {c.name}
                  </span>
                ))}
              </div>
            </div>
          ))}
        </details>
      )}
    </div>
  );
}