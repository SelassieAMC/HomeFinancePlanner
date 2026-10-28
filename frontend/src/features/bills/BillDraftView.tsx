import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { Bill, Category } from '../../types/domain';
import { formatCents } from '../../lib/money';
import { EmptyState, Field } from '../../components/ui';
import { ReceiptPreviewModal } from './ReceiptPreviewModal';

// BillDraftView renders an accepted bill in the same mobile-first style as
// the scan draft: colored summary cards on top, one collapsible panel per
// article. Accepted bills are read-only — editing happens before confirm.
export function BillDraftView({ bill, categories = [] }: { bill: Bill; categories?: Category[] }) {
  const { t } = useTranslation('bills');
  const items = bill.items ?? [];
  const [search, setSearch] = useState('');
  const [showReceipt, setShowReceipt] = useState(false);
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
      const key = c.section || t('legend.other');
      const list = map.get(key) ?? [];
      if (!map.has(key)) map.set(key, list);
      list.push(c);
    }
    return [...map.entries()];
  }, [categories, t]);

  function categoryFor(it: { category_id?: number | null; category_name?: string }) {
    return it.category_id ? categories.find((c) => c.id === it.category_id) : undefined;
  }

  return (
    <div className="bill-draft">
      <div className="stat-cards">
        <div className="stat-card stat-market">
          <span className="stat-card-label">{t('labels.market')}</span>
          <span className="stat-card-value stat-card-text">{bill.market_name || '—'}</span>
          <span className="stat-card-sub">{bill.date || '—'}</span>
        </div>
        <div className="stat-card stat-total">
          <span className="stat-card-label">
            {t('labels.total')}{' '}
            {mismatch && (
              <span
                className="warn-icon"
                title={t('labels.printedMismatch', { amount: formatCents(bill.printed_total_cents, currency) })}
              >
                ⚠️
              </span>
            )}
          </span>
          <span className="stat-card-value">{formatCents(bill.total_cents, currency)}</span>
          <span className="stat-card-sub">
            {mismatch
              ? t('labels.printedShort', { amount: formatCents(bill.printed_total_cents, currency) })
              : t('labels.vatIncluded')}
          </span>
        </div>
        <div className="stat-card stat-account">
          <span className="stat-card-label">{t('labels.account')}</span>
          <span className="stat-card-value stat-card-text">
            {bill.account_name || '—'}
          </span>
          <span className="stat-card-sub">
            {bill.account_name
              ? bill.payment_method === 'cash'
                ? t('labels.walletMoney')
                : t('labels.recordedHere')
              : t('labels.noAccountYet')}
          </span>
        </div>
        <div className="stat-card stat-account">
          <span className="stat-card-label">{t('labels.payment')}</span>
          <span className="stat-card-value stat-card-text">
            {bill.payment_method || '—'}
            {bill.card_last_digits ? ` •${bill.card_last_digits}` : ''}
          </span>
        </div>
        {/* The stored receipt: tap opens the zoom viewer (part tabs for
            multi-part receipts). */}
        <div className="stat-card stat-account receipt-card">
          <span className="stat-card-label">{t('editor.receipt')}</span>
          <button
            type="button"
            className="receipt-thumb-btn"
            onClick={() => setShowReceipt(true)}
            title={t('editor.receiptHint')}
            aria-label={t('editor.receipt')}
          >
            <img
              src={`/api/v1/bills/image/${bill.id}`}
              className="receipt-thumb"
              alt={t('editor.receipt')}
            />
          </button>
          <span className="stat-card-sub">
            {(bill.file_count ?? 1) > 1
              ? t('editor.receiptParts', { n: bill.file_count ?? 1 })
              : t('editor.receiptHint')}
          </span>
        </div>
        <div className="stat-card stat-savings">
          <span className="stat-card-label">{t('labels.savings')}</span>
          <span className="stat-card-value">{formatCents(savings, currency)}</span>
          <span className="stat-card-sub">{t('labels.allDiscounts')}</span>
        </div>
      </div>

      {mismatch && (
        <div className="bill-warning">
          ⚠️ {t('view.warning', { computed: formatCents(bill.total_cents, currency), printed: formatCents(bill.printed_total_cents, currency) })}
        </div>
      )}

      {items.length > 1 && (
        <Field label={t('search.label')} className="bill-search-field">
          <input
            className="search-input"
            type="search"
            placeholder={t('search.placeholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label={t('search.aria')}
          />
        </Field>
      )}

      {items.length === 0 ? (
        <EmptyState message={t('view.empty')} />
      ) : visibleItems.length === 0 ? (
        <EmptyState message={t('search.noMatch', { query: search })} />
      ) : (
        <div className="item-panels">
          {visibleItems.map((it) => {
            const cat = categoryFor(it);
            return (
              <details className="item-panel" key={it.id}>
                <summary>
                  {(it.is_return || cat?.icon) && (
                    <span
                      className="item-icon"
                      title={
                        it.is_return
                          ? t('items.depositTitle')
                          : cat?.name || it.category_name || t('items.unclassified')
                      }
                    >
                      {it.is_return ? '♻️' : cat?.icon}
                    </span>
                  )}
                  <span className="item-title">
                    <span className="item-name">{it.name}</span>
                    {it.is_return ? (
                      <span className="item-brand">{t('items.depositBadge')}</span>
                    ) : (
                      <>
                        {it.brand && <span className="item-brand">{it.brand}</span>}
                        {it.standard_name &&
                          it.standard_name.toLowerCase() !== it.name.toLowerCase() && (
                            <span className="item-brand">↳ {it.standard_name}</span>
                          )}
                        {it.generic_name && (
                          <span className="item-brand">· {it.generic_name}</span>
                        )}
                      </>
                    )}
                  </span>
                  <span className="item-price">{formatCents(it.line_total_cents, currency)}</span>
                </summary>
                <div className="item-detail">
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>{t('fields.brand')}</span>
                      <span>{it.brand || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('fields.measure')}</span>
                      <span>{it.unit || '—'}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('fields.qty')}</span>
                      <span>{it.quantity}</span>
                    </div>
                  </div>
                  <div className="item-field-grid">
                    <div className="item-field">
                      <span>{t('fields.unitPrice')}</span>
                      <span>{formatCents(it.unit_price_cents, currency)}</span>
                    </div>
                    <div className="item-field">
                      <span>{t('fields.discount')}</span>
                      <span>
                        {it.discount_cents > 0
                          ? `−${formatCents(it.discount_cents, currency)}`
                          : '—'}
                      </span>
                    </div>
                    <div className="item-field">
                      <span>{t('fields.lineTotal')}</span>
                      <strong>{formatCents(it.line_total_cents, currency)}</strong>
                    </div>
                  </div>
                  <div className="item-field">
                    <span>{t('fields.category')}</span>
                    <span>
                      {cat ? `${cat.icon} ${cat.name}` : it.category_name || t('items.unclassified')}
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
            <span>{t('labels.budget')}</span>
            <span>{bill.budget_name}</span>
          </div>
        )}
        <div className="bill-total-line">
          <span>{t('fields.itemsTotalCount', { count: items.length })}</span>
          <span>{formatCents(bill.items_subtotal_cents, currency)}</span>
        </div>
        <div className="bill-total-line">
          <span>{t('fields.marketDiscount')}</span>
          <span>{formatCents(bill.discount_cents, currency)}</span>
        </div>
        <div className="bill-total-line">
          <span>{t('fields.vat')}</span>
          <span>{formatCents(bill.vat_cents, currency)}</span>
        </div>
        <div className="bill-total-line bill-total-row">
          <span>{t('fields.totalPaidShort')}</span>
          <strong>{formatCents(bill.total_cents, currency)}</strong>
        </div>
      </div>

      {sections.length > 0 && (
        <details className="category-legend">
          <summary>{t('legend.title')}</summary>
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

      {showReceipt && (
        <ReceiptPreviewModal
          src={(part) => `/api/v1/bills/image/${bill.id}?part=${part}`}
          parts={bill.file_count ?? 1}
          onClose={() => setShowReceipt(false)}
        />
      )}
    </div>
  );
}