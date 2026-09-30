import { Fragment, useEffect, useState } from 'react';
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
  Pagination,
  Spinner,
} from '../../components/ui';

const PAGE_SIZE = 20;

const SORT_OPTIONS: { value: ProductSort; label: string }[] = [
  { value: 'name', label: 'Name' },
  { value: 'updated_at', label: 'Recently updated' },
  { value: 'times_bought', label: 'Times bought' },
  { value: 'last_purchase', label: 'Last purchase' },
  { value: 'avg_price', label: 'Average price' },
];

/**
 * Products grouped by their generic product family: every row is one family,
 * expandable into a mini table of its member products (raw + standardized
 * name, latest price, last store). The name search and the sorting work on
 * the group's generic name; the price column is the family average.
 */
export function ProductsPage() {
  const categories = useAsync(() => categoriesApi.list(), []);

  // Filters. The name input is debounced; everything else commits directly.
  const [nameInput, setNameInput] = useState('');
  const [name, setName] = useState('');
  const [categoryId, setCategoryId] = useState<number | null>(null);
  const [sort, setSort] = useState<ProductSort>('name');
  const [order, setOrder] = useState<'asc' | 'desc'>('asc');
  const [offset, setOffset] = useState(0);

  useEffect(() => {
    const t = setTimeout(() => {
      setName(nameInput.trim());
      setOffset(0);
    }, 300);
    return () => clearTimeout(t);
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
    const t = setTimeout(() => setJustAdded(null), 1200);
    return () => clearTimeout(t);
  }, [justAdded]);

  const groups = data?.items ?? [];
  const allProducts = groups.flatMap((g) => g.items);

  if (loading && !data) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const editing = allProducts.find((p) => p.id === editingId) ?? null;

  return (
    <div className="page">
      <h2 className="page-title">Products</h2>

      <div className="filter-row">
        <input
          placeholder="Search generic name"
          value={nameInput}
          onChange={(e) => setNameInput(e.target.value)}
        />
        <CategorySelect
          categories={categories.data ?? []}
          kind="product"
          value={categoryId}
          ariaLabel="Filter by category"
          emptyLabel="All categories"
          onChange={(id) => {
            setCategoryId(id);
            setOffset(0);
          }}
        />
        <select
          value={sort}
          aria-label="Sort by"
          onChange={(e) => {
            setSort(e.target.value as ProductSort);
            setOffset(0);
          }}
        >
          {SORT_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              Sort: {o.label}
            </option>
          ))}
        </select>
        <button
          className="btn btn-secondary sort-order"
          onClick={() => {
            setOrder(order === 'asc' ? 'desc' : 'asc');
            setOffset(0);
          }}
          title={order === 'asc' ? 'Ascending' : 'Descending'}
        >
          {order === 'asc' ? '↑' : '↓'}
        </button>
      </div>

      {groups.length === 0 ? (
        <EmptyState
          message={
            name || categoryId
              ? 'No products match these filters.'
              : 'No products yet — confirm a scanned bill and its items appear here.'
          }
        />
      ) : (
        <div className="table-scroll">
          <table className="data-table products-table">
            <thead>
              <tr>
                <th>Product</th>
                <th>Category</th>
                <th className="num">Avg price</th>
                <th aria-label="Actions" />
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
                            title={isOpen ? 'Hide the products of this family' : 'Show the products of this family'}
                            aria-label={`${isOpen ? 'Hide' : 'Show'} the products of ${group.generic_name}`}
                            aria-expanded={isOpen}
                            onClick={() => toggleExpanded(key)}
                          >
                            {isOpen ? '▾' : '▸'}
                          </button>
                          <span className="product-name">{group.generic_name}</span>
                          <span
                            className="group-count"
                            title={`${group.product_count} product${group.product_count === 1 ? '' : 's'} in this family`}
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
                            className="icon-btn"
                            title="Edit the generic product name of this family"
                            aria-label={`Edit the generic product name of ${group.generic_name}`}
                            onClick={() => setEditingGroup(group)}
                          >
                            ✏️
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
                                <th>Product</th>
                                <th>Standardized name</th>
                                <th className="num">Latest price</th>
                                <th>Store</th>
                                <th aria-label="Actions" />
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
                                        className="icon-btn"
                                        title="View details"
                                        aria-label={`View details of ${product.name}`}
                                        onClick={() => setDetailsProduct(product)}
                                      >
                                        🔍
                                      </button>
                                      <button
                                        type="button"
                                        className="icon-btn"
                                        title={cart.has(product.id) ? 'Already in the cart' : 'Add to the purchase cart'}
                                        aria-label={`Add ${product.name} to the purchase cart`}
                                        onClick={() => {
                                          cart.add(product);
                                          setJustAdded(product.id);
                                        }}
                                      >
                                        {justAdded === product.id ? '✅' : '🛒'}
                                      </button>
                                      <button
                                        type="button"
                                        className="icon-btn"
                                        title="Edit product"
                                        aria-label={`Edit ${product.name}`}
                                        onClick={() => setEditingId(product.id)}
                                      >
                                        ✏️
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