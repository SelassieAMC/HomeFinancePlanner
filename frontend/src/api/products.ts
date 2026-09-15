import { apiClient } from './client';
import type {
  Paged,
  Product,
  ProductFilters,
  ProductInput,
  ProductStorePrice,
} from '../types/domain';

const BASE = '/api/v1';

function toQuery(filters: ProductFilters): string {
  const params = new URLSearchParams();
  // `!== undefined` everywhere: offset 0 must still be sent.
  if (filters.name) params.set('name', filters.name);
  if (filters.category_id !== undefined) params.set('category_id', String(filters.category_id));
  if (filters.sort) params.set('sort', filters.sort);
  if (filters.order) params.set('order', filters.order);
  if (filters.limit !== undefined) params.set('limit', String(filters.limit));
  if (filters.offset !== undefined) params.set('offset', String(filters.offset));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

export const productsApi = {
  list: (filters: ProductFilters = {}) =>
    apiClient.get<Paged<Product>>(`${BASE}/products${toQuery(filters)}`),
  get: (id: number) => apiClient.get<Product>(`${BASE}/products/${id}`),
  /** Latest price per store (details modal), scoped to the product's latest
   *  purchase currency. */
  storePrices: (id: number) =>
    apiClient.get<ProductStorePrice[]>(`${BASE}/products/${id}/prices`),
  /** Replaces name/brand/unit/category/description; also rewrites the
   *  historical bill items linked to the product (name/unit/category). */
  update: (id: number, input: ProductInput) =>
    apiClient.put<Product>(`${BASE}/products/${id}`, input),
  uploadPhoto: (id: number, file: File) => {
    const form = new FormData();
    form.append('photo', file);
    return apiClient.postForm<Product>(`${BASE}/products/${id}/photo`, form);
  },
  removePhoto: (id: number) => apiClient.delete<Product>(`${BASE}/products/${id}/photo`),
  /** Cache-busted photo URL for <img>; empty string when the product has none.
   *  Photo writes bump updated_at, so it doubles as a cache buster. */
  photoUrl: (product: Product) =>
    product.has_image ? `/api/v1/products/${product.id}/photo?v=${product.updated_at}` : '',
};