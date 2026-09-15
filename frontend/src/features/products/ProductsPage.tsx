import { useEffect, useState } from 'react';
import { useAsync } from '../../hooks/useAsync';
import { productsApi } from '../../api/products';
import { categoriesApi } from '../../api/categories';
import { formatCents } from '../../lib/money';
import type { Product, ProductSort } from '../../types/domain';
import { ProductDetailsModal } from './ProductDetailsModal';
import { ProductEditModal } from './ProductEditModal';
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
  { value: 'best_price', label: 'Best price' },
];

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
      productsApi.list({
        name: name || undefined,
        category_id: categoryId ?? undefined,
        sort,
        order,
        limit: PAGE_SIZE,
        offset,
      }),
    [name, categoryId, sort, order, offset],
  );

  // One product edited full-page at a time, one viewed in the details modal.
  const [editingId, setEditingId] = useState<number | null>(null);
  const [detailsProduct, setDetailsProduct] = useState<Product | null>(null);

  const products = data?.items ?? [];

  if (loading && !data) return <Spinner />;
  if (error) return <ErrorMessage message={error.message} />;

  const editing = products.find((p) => p.id === editingId) ?? null;

  return (
    <div className="page">
      <h2 className="page-title">Products</h2>

      <div className="filter-row">
        <input
          placeholder="Search name"
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

      {products.length === 0 ? (
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
                <th className="num">Best price</th>
                <th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {products.map((product) => (
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
                  <td>{product.category_name || '—'}</td>
                  <td className="num">
                    {product.best_price_cents !== undefined && product.price_currency
                      ? formatCents(product.best_price_cents, product.price_currency)
                      : '—'}
                  </td>
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