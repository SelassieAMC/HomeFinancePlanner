import { apiClient } from './client';
import type { Account, AccountGroup, AccountInput, AccountLedger } from '../types/domain';

const BASE = '/api/v1';

export const accountsApi = {
  list: () => apiClient.get<Account[]>(`${BASE}/accounts`),
  get: (id: number) => apiClient.get<Account>(`${BASE}/accounts/${id}`),
  create: (input: AccountInput) => apiClient.post<Account>(`${BASE}/accounts`, input),
  update: (id: number, input: AccountInput) =>
    apiClient.put<Account>(`${BASE}/accounts/${id}`, input),
  remove: (id: number) => apiClient.delete(`${BASE}/accounts/${id}`),
  // Align the derived balance with what the bank reports (adjusts the opening
  // balance; the transaction ledger stays untouched).
  reconcile: (id: number, balanceCents: number) =>
    apiClient.post<Account>(`${BASE}/accounts/${id}/reconcile`, {
      balance_cents: balanceCents,
    }),
  // The register: the account plus its transaction thread (newest first,
  // limit ≤ 200, offset pages backwards), every row with the balance as it
  // stood right after it.
  ledger: (id: number, limit?: number, offset?: number) =>
    apiClient.get<AccountLedger>(
      `${BASE}/accounts/${id}/ledger${limit != null || offset != null ? '?' : ''}` +
        (limit != null ? `limit=${limit}` : '') +
        (offset != null ? `${limit != null ? '&' : ''}offset=${offset}` : ''),
    ),
};

// The presentation-only account groups (folders): a group is a name, the
// membership lives on the account rows, and deleting a group just ungroups.
export const accountGroupsApi = {
  list: () => apiClient.get<AccountGroup[]>(`${BASE}/accounts/groups`),
  create: (name: string) => apiClient.post<AccountGroup>(`${BASE}/accounts/groups`, { name }),
  update: (id: number, name: string) =>
    apiClient.put<AccountGroup>(`${BASE}/accounts/groups/${id}`, { name }),
  remove: (id: number) => apiClient.delete(`${BASE}/accounts/groups/${id}`),
};