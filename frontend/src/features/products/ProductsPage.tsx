import { Fragment, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsync } from '../../hooks/useAsync';
import { productsApi } from '../../api/products';
import { categoriesApi } from '../../api/categories';
import { formatCents } from '../../lib/money';
import type { Product, ProductGroup, ProductSort } from '../../types/domain';
import { ProductDetailsModal } from './ProductDetailsModal';
import { ProductEditModal } from './ProductEditModal';
import { ProductGroupEditModal } from './ProductGroupEditModal';
import { useCart } from '../cart/CartContext';
import {
  CategorySelect,
  EmptyState,
  ErrorMessage,
  Field,
  Pagination,
  Spinner,
} from '../../components/ui';

const PAGE_SIZE = 20;

const SORT_OPTIONS: { value: ProductSort; labelKey: string }[] = [
  { value: 'name', labelKey: 'sort.name' },
  { value: 'updated_at', labelKey: 'sort.recentlyUpdated' },
  { value: 'times_bought', labelKey: 'sort.timesBought' },
  { value: 'last_purchase', labelKey: 'sort.lastPurchase' },
  { value: 'avg_price', labelKey: 'sort.avgPrice' },
];

/**
 * Products grouped by their generic product family: every row is one family,
 * expandable into a mini table of its member products (raw + standardized
 * name, latest price, last store). The name search and the sorting work on
 * the group's generic name; the price column is the family average.
 */
export function ProductsPage() {
  const { t } = useTranslation('products');
  const categories = useAsync(() => categoriesApi.list(), []);

  // Filters. The name input is debounced; everything else commits directly.
  const [nameInput, setNameInput] = useState('');
  const [name, setName] = useState('');
  const [categoryId, setCategoryId] = useState<number | null>(null);
  const [sort, setSort] = useState<ProductSort>('name');
  const [order, setOrder] = useState<'asc' | 'desc'>('asc');
  const [offset, setOffset] = useState(0);

  useEffect(() => {
    const timer = setTimeout(() => {
      setName(nameInput.trim());
      setOffset(0);
    }, 300);
    return () => clearTimeout(timer);
  }, [nameInput]);

  const { data, loading, error, reload } = useAsync(
    () =>
      productsApi.listGrouped({
        name: name || undefined,
        category_id: categoryId ?? undefined,
        sort,
        order,
        limit: PAGE_SIZE,
        offset,
      }),
    [name, categoryId, sort, order, offset],
  );

  // Which family rows are expanded (their mini table is visible).
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const toggleExpanded = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };

  // One generic family edited at a time in its own modal, one product edited
  // full-page at a time, one viewed in the details modal.
  const [editingGroup, setEditingGroup] = useState<ProductGroup | null>(null);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [detailsProduct, setDetailsProduct] = useState<Product | null>(null);
  // Purchase cart: the just-added row flips its icon for a moment as feedback
  // (there is no toast system; the transient icon flip is the confirmation).
  const cart = useCart();
  const [justAdded, setJustAdded] = useState<number | null>(null);
  useEffect(() => {
    if (justAdded === null) return;
    const timer = setTimeout(() => setJustAdded(null), 1200);
    return () => clearTimeout(timer);
  }, [justAdded]);

  const groups = data?.items ?? [];
  const allProducts = groups.flatMap((g) => g.items);

  if (loading && !data) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const editing = allProducts.find((p) => p.id === editingId) ?? null;

  return (
    <div className="page">
      <h2 className="page-title">{t('title')}</h2>

      <div className="filter-row">
        <Field label={t('search.label')} layout="row">
          <input
            placeholder={t('search.placeholder')}
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            aria-label={t('search.label')}
          />
        </Field>
        <Field label={t('search.categoryAria')} layout="row">
          <CategorySelect
            categories={categories.data ?? []}
            kind="product"
            value={categoryId}
            ariaLabel={t('search.categoryAria')}
            emptyLabel={t('search.allCategories')}
            onChange={(id) => {
              setCategoryId(id);
              setOffset(0);
            }}
          />
        </Field>
        <Field label={t('sort.orderBy')} layout="row">
          <select
            value={sort}
            aria-label={t('sort.orderBy')}
            onChange={(e) => {
              setSort(e.target.value as ProductSort);
              setOffset(0);
            }}
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {t('sort.prefix')} {t(o.labelKey)}
              </option>
            ))}
          </select>
        </Field>
        <button
          className="btn btn-secondary sort-order"
          onClick={() => {
            setOrder(order === 'asc' ? 'desc' : 'asc');
            setOffset(0);
          }}
          title={order === 'asc' ? t('sort.ascending') : t('sort.descending')}
        >
          {order === 'asc' ? '↑' : '↓'}
        </button>
      </div>

      {groups.length === 0 ? (
        <EmptyState
          message={
            name || categoryId
              ? t('empty.filtered')
              : t('empty.none')
          }
        />
      ) : (
        <div className="table-scroll">
          <table className="data-table products-table">
            <thead>
              <tr>
                <th>{t('columns.product')}</th>
                <th>{t('columns.category')}</th>
                <th className="num">{t('columns.avgPrice')}</th>
                <th aria-label={t('columns.actions')} />
              </tr>
            </thead>
            <tbody>
              {groups.map((group) => {
                const key = group.generic_name;
                const isOpen = expanded.has(key);
                return (
                  <Fragment key={key}>
                    <tr>
                      <td>
                        <div className="product-cell">
                          <button
                            type="button"
                            className="icon-btn group-toggle"
                            title={isOpen ? t('family.hide') : t('family.show')}
                            aria-label={isOpen ? t('family.ariaHide', { name: group.generic_name }) : t('family.ariaShow', { name: group.generic_name })}
                            aria-expanded={isOpen}
                            onClick={() => toggleExpanded(key)}
                          >
                            {isOpen ? '▾' : '▸'}
                          </button>
                          <span className="product-name">{group.generic_name}</span>
                          <span
                            className="group-count"
                            title={t('family.countTitle', { count: group.product_count })}
                          >
                            {group.product_count}
                          </span>
                        </div>
                      </td>
                      <td>{group.category_name || '—'}</td>
                      <td className="num">
                        {group.avg_price_cents !== undefined && group.price_currency
                          ? formatCents(group.avg_price_cents, group.price_currency)
                          : '—'}
                      </td>
                      <td>
                        <div className="row-actions">
                          <button
                            type="button"
                            className="btn btn-ghost btn-sm"
                            title={t('family.renameTitle')}
                            aria-label={t('family.renameAria', { name: group.generic_name })}
                            onClick={() => setEditingGroup(group)}
                          >
                            {t('actions.rename')}
                          </button>
                        </div>
                      </td>
                    </tr>
                    {isOpen && (
                      <tr className="group-members">
                        <td colSpan={4}>
                          <table className="data-table group-members-table">
                            <thead>
                              <tr>
                                <th>{t('columns.product')}</th>
                                <th>{t('columns.standardName')}</th>
                                <th className="num">{t('columns.latestPrice')}</th>
                                <th>{t('columns.store')}</th>
                                <th aria-label={t('columns.actions')} />
                              </tr>
                            </thead>
                            <tbody>
                              {group.items.map((product) => (
                                <tr key={product.id}>
                                  <td>
                                    <div className="product-cell">
                                      {product.has_image && (
                                        <img
                                          className="product-photo product-photo-small"
                                          src={productsApi.photoUrl(product)}
                                          alt=""
                                        />
                                      )}
                                      <span className="product-name">{product.name}</span>
                                    </div>
                                  </td>
                                  <td>
                                    {product.standard_name &&
                                    product.standard_name.toLowerCase() !== product.name.toLowerCase()
                                      ? product.standard_name
                                      : '—'}
                                  </td>
                                  <td className="num">
                                    {product.latest_price_cents !== undefined && product.price_currency
                                      ? formatCents(product.latest_price_cents, product.price_currency)
                                      : '—'}
                                  </td>
                                  <td>{product.last_store_name || '—'}</td>
                                  <td>
                                    <div className="row-actions">
                                      <button
                                        type="button"
                                        className="btn btn-ghost btn-sm"
                                        title={t('actions.detailsTitle')}
                                        aria-label={t('actions.detailsAria', { name: product.name })}
                                        onClick={() => setDetailsProduct(product)}
                                      >
                                        {t('actions.details')}
                                      </button>
                                      <button
                                        type="button"
                                        className="btn btn-ghost btn-sm"
                                        title={cart.has(product.id) ? t('addToCart.titleInCart') : t('addToCart.title')}
                                        aria-label={t('addToCart.aria', { name: product.name })}
                                        onClick={() => {
                                          cart.add(product);
                                          setJustAdded(product.id);
                                        }}
                                      >
                                        {justAdded === product.id
                                          ? t('addToCart.added')
                                          : cart.has(product.id)
                                            ? t('addToCart.add')
                                            : t('addToCart.addLong')}
                                      </button>
                                      <button
                                        type="button"
                                        className="btn btn-ghost btn-sm"
                                        title={t('actions.editTitle')}
                                        aria-label={t('actions.editAria', { name: product.name })}
                                        onClick={() => setEditingId(product.id)}
                                      >
                                        {t('actions.edit')}
                                      </button>
                                    </div>
                                  </td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <Pagination
        total={data?.total ?? 0}
        limit={PAGE_SIZE}
        offset={data?.offset ?? offset}
        onPage={setOffset}
      />

      {detailsProduct && (
        <ProductDetailsModal product={detailsProduct} onClose={() => setDetailsProduct(null)} />
      )}

      {editingGroup && (
        <ProductGroupEditModal
          group={editingGroup}
          onClose={() => setEditingGroup(null)}
          onSaved={() => {
            setEditingGroup(null);
            reload();
          }}
        />
      )}

      {editing && (
        <ProductEditModal
          key={editing.id}
          product={editing}
          categories={categories.data ?? []}
          onClose={() => setEditingId(null)}
          onSaved={() => {
            setEditingId(null);
            reload();
          }}
        />
      )}
    </div>
  );
}