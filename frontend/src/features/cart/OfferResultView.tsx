import { useTranslation } from 'react-i18next';
import i18n from '../../i18n';
import type { OfferResult } from '../../types/domain';
import { formatCents } from '../../lib/money';
import { EmptyState } from '../../components/ui';

// OfferResultView renders a persisted offer search read-only, in the same
// style as the accepted-bill view: one collapsible panel per product with an
// offers table inside. Best price per product is tinted green, worst red
// (flags computed backend-side, per product and currency).
export function OfferResultView({ result }: { result: OfferResult }) {
  const { t } = useTranslation('cart');
  // Older persisted rows can carry null instead of an empty list (the model
  // omitted the field and Go marshalled its nil slice as null) — guard reads.
  const products = result.products ?? [];
  return (
    <div className="bill-draft">
      <p className="hint-text">
        {t('view.searchedOn', { date: new Date(result.searched_at).toLocaleString() })}
      </p>

      {products.length === 0 ? (
        <EmptyState message={t('view.noData')} />
      ) : (
        <div className="item-panels">
          {products.map((product) => {
            const offers = product.offers ?? [];
            return (
            <details className="item-panel" key={product.product_id} open>
              <summary>
                <span className="item-title">
                  <span className="item-name">{product.name}</span>
                  {product.brand && <span className="item-brand">{product.brand}</span>}
                </span>
                <span className="item-price">
                  {offers.length > 0
                    ? bestPriceLabel(offers)
                    : t('view.noOffers')}
                </span>
              </summary>
              <div className="item-detail">
                {product.note && <p className="hint-text">{product.note}</p>}
                {offers.length === 0 ? (
                  <p className="hint-text">{t('view.nothingFound')}</p>
                ) : (
                  <div className="offer-table-scroll">
                    <table className="data-table offer-table">
                      <thead>
                        <tr>
                          <th>{t('view.market')}</th>
                          <th>{t('view.brand')}</th>
                          <th className="num">{t('view.price')}</th>
                          <th>{t('view.offerQuestion')}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {offers.map((o, i) => {
                          // Pinned-scope searches discriminate stores without a
                          // price: not available / not published instead of one.
                          const unavailable =
                            o.availability === 'not_available' || o.availability === 'not_published';
                          return (
                            <tr
                              key={`${o.market}-${o.brand}-${i}`}
                              className={
                                unavailable
                                  ? 'offer-unavailable'
                                  : o.best_price
                                    ? 'offer-best'
                                    : o.worst_price
                                      ? 'offer-worst'
                                      : undefined
                              }
                            >
                              <td>
                                {o.market}
                                {o.variety && <span className="offer-variety">{o.variety}</span>}
                              </td>
                              <td>{o.brand || '—'}</td>
                              <td className="num">
                                {unavailable
                                  ? o.availability === 'not_available'
                                    ? t('view.availabilityNotAvailable')
                                    : t('view.availabilityNotPublished')
                                  : formatCents(o.price_cents, o.currency)}
                              </td>
                              <td>{o.is_offer ? <span className="offer-tag">{t('view.offerTag')}</span> : '—'}</td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                {offers.some((o) => o.note) && (
                  <ul className="offer-notes">
                    {offers
                      .filter((o) => o.note)
                      .map((o, i) => (
                        <li key={i}>
                          {o.market}: {o.note}
                        </li>
                      ))}
                  </ul>
                )}
              </div>
            </details>
            );
          })}
        </div>
      )}
    </div>
  );
}

/** Formats the cheapest offer's price for the collapsed panel summary.
 *  Unavailable rows (0 cents) never count as the cheapest. */
function bestPriceLabel(offers: { price_cents: number; currency: string; variety?: string }[]): string {
  const priced = offers.filter((o) => o.price_cents > 0);
  if (priced.length === 0) return i18n.t('cart:view.notFound');
  const cheapest = priced.reduce((min, o) => (o.price_cents < min.price_cents ? o : min));
  const variety = cheapest.variety ? ` (${cheapest.variety})` : '';
  return `${i18n.t('cart:view.bestPrice', { price: formatCents(cheapest.price_cents, cheapest.currency) })}${variety}`;
}