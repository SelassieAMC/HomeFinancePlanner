import { apiClient } from './client';
import type { Store, StoreInput } from '../types/domain';

const BASE = '/api/v1';

export const storesApi = {
  list: () => apiClient.get<Store[]>(`${BASE}/stores`),
  get: (id: number) => apiClient.get<Store>(`${BASE}/stores/${id}`),
  create: (input: StoreInput) => apiClient.post<Store>(`${BASE}/stores`, input),
  /** Replaces name/description/location; the logo is untouched. */
  update: (id: number, input: StoreInput) =>
    apiClient.put<Store>(`${BASE}/stores/${id}`, input),
  /** Removes the store; past bills keep their market_name snapshot. */
  remove: (id: number) => apiClient.delete(`${BASE}/stores/${id}`),
  uploadLogo: (id: number, file: File) => {
    const form = new FormData();
    form.append('logo', file);
    return apiClient.postForm<Store>(`${BASE}/stores/${id}/logo`, form);
  },
  removeLogo: (id: number) => apiClient.delete<Store>(`${BASE}/stores/${id}/logo`),
  /** Cache-busted logo URL for <img>; empty string when the store has none.
   *  Logo writes bump updated_at, so it doubles as a cache buster. */
  logoUrl: (store: Store) =>
    store.has_logo ? `/api/v1/stores/${store.id}/logo?v=${store.updated_at}` : '',
};