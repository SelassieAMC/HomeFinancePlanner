import { apiClient } from './client';
import type { OfferSearch, OfferSearchStatus } from '../types/domain';

const BASE = '/api/v1';

/** One confirmed cart line: the product plus an optional brand preference. */
export interface CartSearchInputItem {
  product_id: number;
  quantity?: number;
  brand?: string;
}

/** Search scope: pinned stores (empty/absent = any local market) and the
 *  name-match mode (strict = exact names, loose = include varieties). */
export interface CartSearchInput {
  items: CartSearchInputItem[];
  stores?: string[];
  name_match?: 'strict' | 'loose';
}

export const cartApi = {
  /** Confirm the cart: starts the offer search with the default AI provider.
   *  Returns immediately with status "searching"; poll get until done/failed. */
  search: (input: CartSearchInput) =>
    apiClient.post<OfferSearch>(`${BASE}/cart-searches`, input),
  /** Poll one search's pipeline state (searching → done/failed). */
  get: (token: string) => apiClient.get<OfferSearch>(`${BASE}/cart-searches/${token}`),
  /** Recent searches for the purchase-cart view. */
  list: (statuses?: OfferSearchStatus[]) => {
    const qs = statuses?.length ? `?status=${statuses.join(',')}` : '';
    return apiClient.get<OfferSearch[]>(`${BASE}/cart-searches${qs}`);
  },
  /** Re-run a finished/failed search (returns to "searching"). */
  retry: (token: string, providerId?: string) =>
    apiClient.post<OfferSearch>(`${BASE}/cart-searches/${token}/retry`, {
      provider_id: providerId || undefined,
    }),
  /** Discard a search row and its persisted result. */
  remove: (token: string) => apiClient.delete(`${BASE}/cart-searches/${token}`),
};