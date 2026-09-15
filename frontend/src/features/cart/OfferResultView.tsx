import type { OfferResult } from '../../types/domain';
import { formatCents } from '../../lib/money';
import { EmptyState } from '../../components/ui';

// OfferResultView renders a persisted offer search read-only, in the same
// style as the accepted-bill view: one collapsible panel per product with an
// offers table inside. Best price per product is tinted green, worst red
// (flags computed backend-side, per product and currency).
export function OfferResultView({ result }: { result: OfferResult }) {
  return (
    <div className="bill-draft">
      <p className="hint-text">
        Offers searched on {new Date(result.searched_at).toLocaleString()} — green marks the best
        price, red the worst (per product and currency).
      </p>

      {result.products.length === 0 ? (
        <EmptyState message="No offer data came back for the cart." />
      ) : (
        <div className="item-panels">
          {result.products.map((product) => (
            <details className="item-panel" key={product.product_id} open>
              <summary>
                <span className="item-icon" aria-hidden="true">
                  🛒
                </span>
                <span className="item-title">
                  <span className="item-name">{product.name}</span>
                  {product.brand && <span className="item-brand">{product.brand}</span>}
                </span>
                <span className="item-price">
                  {product.offers.length > 0
                    ? bestPriceLabel(product.offers)
                    : 'no offers'}
                </span>
              </summary>
              <div className="item-detail">
                {product.note && <p className="hint-text">{product.note}</p>}
                {product.offers.length === 0 ? (
                  <p className="hint-text">Nothing was found for this product.</p>
                ) : (
                  <div className="offer-table-scroll">
                    <table className="data-table offer-table">
                      <thead>
                        <tr>
                          <th>Market</th>
                          <th>Brand</th>
                          <th className="num">Price</th>
                          <th>Offer?</th>
                        </tr>
                      </thead>
                      <tbody>
                        {product.offers.map((o, i) => (
                          <tr
                            key={`${o.market}-${o.brand}-${i}`}
                            className={
                              o.best_price ? 'offer-best' : o.worst_price ? 'offer-worst' : undefined
                            }
                          >
                            <td>{o.market}</td>
                            <td>{o.brand || '—'}</td>
                            <td className="num">{formatCents(o.price_cents, o.currency)}</td>
                            <td>{o.is_offer ? <span className="offer-tag">offer</span> : '—'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                {product.offers.some((o) => o.note) && (
                  <ul className="offer-notes">
                    {product.offers
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
          ))}
        </div>
      )}
    </div>
  );
}

/** Formats the cheapest offer's price for the collapsed panel summary. */
function bestPriceLabel(offers: { price_cents: number; currency: string }[]): string {
  const cheapest = offers.reduce((min, o) => (o.price_cents < min.price_cents ? o : min));
  return `from ${formatCents(cheapest.price_cents, cheapest.currency)}`;
}