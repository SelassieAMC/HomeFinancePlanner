import { useAsync } from '../../hooks/useAsync';
import { productsApi } from '../../api/products';
import { formatCents } from '../../lib/money';
import type { Product } from '../../types/domain';
import { Button, Dialog, ErrorMessage, Spinner } from '../../components/ui';

/**
 * Read-only product details: the purchase stats plus a per-store breakdown of
 * the latest price paid (a product can be bought at different stores over
 * time). All amounts are in the product's most recent purchase currency.
 */
export function ProductDetailsModal({
  product,
  onClose,
}: {
  product: Product;
  onClose: () => void;
}) {
  const prices = useAsync(() => productsApi.storePrices(product.id), [product.id]);
  const currency = product.price_currency ?? '';

  return (
    <Dialog title={product.name}>
      {product.has_image && (
        <img className="product-photo" src={productsApi.photoUrl(product)} alt="" />
      )}
      {product.brand && <p className="product-details-brand">{product.brand}</p>}
      {product.description && <p className="product-details-description">{product.description}</p>}

      <div className="item-field-grid">
        <div className="item-field">
          <span>Measure</span>
          <span>{product.unit || '—'}</span>
        </div>
        <div className="item-field">
          <span>Best price</span>
          <strong>
            {product.best_price_cents !== undefined && currency
              ? formatCents(product.best_price_cents, currency)
              : '—'}
          </strong>
        </div>
        <div className="item-field">
          <span>Last purchased</span>
          <span>{product.last_purchase_date || '—'}</span>
        </div>
        <div className="item-field">
          <span>Times bought</span>
          <strong>{product.times_bought}</strong>
        </div>
      </div>

      <h4 className="product-details-heading">Prices by store</h4>
      {prices.loading && !prices.data ? (
        <Spinner label="Loading store prices…" />
      ) : prices.error ? (
        <ErrorMessage message={prices.error.message} />
      ) : (prices.data ?? []).length === 0 ? (
        <p className="product-details-empty">No store prices recorded yet.</p>
      ) : (
        <div className="table-scroll">
          <table className="data-table store-prices-table">
            <thead>
              <tr>
                <th>Store</th>
                <th className="num">Latest price</th>
                <th>Last bought</th>
              </tr>
            </thead>
            <tbody>
              {(prices.data ?? []).map((row) => (
                <tr key={row.store_id ?? 'none'}>
                  <td>{row.store_name}</td>
                  <td className="num">
                    {row.latest_price_cents !== undefined &&
                    row.latest_price_cents !== null &&
                    currency
                      ? formatCents(row.latest_price_cents, currency)
                      : '—'}
                  </td>
                  <td>{row.last_purchase_date || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="dialog-actions">
        <Button variant="ghost" onClick={onClose}>
          Close
        </Button>
      </div>
    </Dialog>
  );
}