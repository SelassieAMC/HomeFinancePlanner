import { useTranslation } from 'react-i18next';
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
  const { t } = useTranslation('products');
  const prices = useAsync(() => productsApi.storePrices(product.id), [product.id]);
  const currency = product.price_currency ?? '';

  // Price per unit of measure from the average pack price ÷ printed size;
  // g/ml are scaled up to their per-kg/per-l figure (like the analytics rule).
  function pricePerUnit(p: Product): string | null {
    const unit = p.unit?.toLowerCase();
    if (
      !unit ||
      !p.unit_value ||
      p.unit_value <= 0 ||
      p.avg_price_cents === undefined ||
      !p.price_currency
    ) {
      return null;
    }
    const scaled = unit === 'g' || unit === 'ml';
    const per = (p.avg_price_cents / p.unit_value) * (scaled ? 1000 : 1);
    return `${formatCents(Math.round(per), p.price_currency)} / ${scaled ? (unit === 'g' ? 'kg' : 'l') : unit}`;
  }
  const perUnit = pricePerUnit(product);

  return (
    <Dialog title={product.name}>
      {product.has_image && (
        <img className="product-photo" src={productsApi.photoUrl(product)} alt="" />
      )}
      {product.brand && <p className="product-details-brand">{product.brand}</p>}
      {product.description && <p className="product-details-description">{product.description}</p>}

      <div className="item-field-grid">
        <div className="item-field">
          <span>{t('labels.measure')}</span>
          <span>{product.unit ? (product.unit_value ? `${product.unit_value} ${product.unit}` : product.unit) : '—'}</span>
        </div>
        <div className="item-field">
          <span>{t('labels.bestPrice')}</span>
          <strong>
            {product.best_price_cents !== undefined && currency
              ? formatCents(product.best_price_cents, currency)
              : '—'}
          </strong>
        </div>
        {perUnit && (
          <div className="item-field">
            <span>{t('labels.pricePerUnit')}</span>
            <strong>{perUnit}</strong>
          </div>
        )}
        <div className="item-field">
          <span>{t('labels.lastPurchased')}</span>
          <span>{product.last_purchase_date || '—'}</span>
        </div>
        <div className="item-field">
          <span>{t('labels.timesBought')}</span>
          <strong>{product.times_bought}</strong>
        </div>
      </div>

      <h4 className="product-details-heading">{t('details.pricesByStore')}</h4>
      {prices.loading && !prices.data ? (
        <Spinner label={t('details.loadingPrices')} />
      ) : prices.error ? (
        <ErrorMessage message={prices.error.message} />
      ) : (prices.data ?? []).length === 0 ? (
        <p className="product-details-empty">{t('details.noPrices')}</p>
      ) : (
        <div className="table-scroll">
          <table className="data-table store-prices-table">
            <thead>
              <tr>
                <th>{t('columns.store')}</th>
                <th className="num">{t('columns.latestPrice')}</th>
                <th>{t('columns.lastBought')}</th>
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
          {t('actions.close')}
        </Button>
      </div>
    </Dialog>
  );
}