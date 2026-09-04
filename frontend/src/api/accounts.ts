import { apiClient } from './client';
import type { Account, AccountInput } from '../types/domain';

const BASE = '/api/v1';

export const accountsApi = {
  list: () => apiClient.get<Account[]>(`${BASE}/accounts`),
  get: (id: number) => apiClient.get<Account>(`${BASE}/accounts/${id}`),
  create: (input: AccountInput) => apiClient.post<Account>(`${BASE}/accounts`, input),
  update: (id: number, input: AccountInput) =>
    apiClient.put<Account>(`${BASE}/accounts/${id}`, input),
  remove: (id: number) => apiClient.delete(`${BASE}/accounts/${id}`),
};