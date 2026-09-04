import { apiClient } from './client';
import type { Transaction, TransactionInput } from '../types/domain';

const BASE = '/api/v1';

export interface TransactionFilters {
  account_id?: number;
  category_id?: number;
  month?: string;
  kind?: string;
  limit?: number;
  offset?: number;
}

function toQuery(filters: TransactionFilters): string {
  const params = new URLSearchParams();
  if (filters.account_id) params.set('account_id', String(filters.account_id));
  if (filters.category_id) params.set('category_id', String(filters.category_id));
  if (filters.month) params.set('month', filters.month);
  if (filters.kind) params.set('kind', filters.kind);
  if (filters.limit) params.set('limit', String(filters.limit));
  if (filters.offset) params.set('offset', String(filters.offset));
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

export const transactionsApi = {
  list: (filters: TransactionFilters = {}) =>
    apiClient.get<Transaction[]>(`${BASE}/transactions${toQuery(filters)}`),
  get: (id: number) => apiClient.get<Transaction>(`${BASE}/transactions/${id}`),
  create: (input: TransactionInput) =>
    apiClient.post<Transaction>(`${BASE}/transactions`, input),
  update: (id: number, input: TransactionInput) =>
    apiClient.put<Transaction>(`${BASE}/transactions/${id}`, input),
  remove: (id: number) => apiClient.delete(`${BASE}/transactions/${id}`),
};