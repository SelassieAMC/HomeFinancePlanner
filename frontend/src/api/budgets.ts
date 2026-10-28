import { apiClient } from './client';
import type { Budget, BudgetInput, BudgetLifecycle } from '../types/domain';

const BASE = '/api/v1';

export const budgetsApi = {
  /** All envelopes; pass a lifecycle status to filter ("open" / "closed"). */
  list: (status?: BudgetLifecycle) =>
    apiClient.get<Budget[]>(`${BASE}/budgets${status ? `?status=${status}` : ''}`),
  create: (input: BudgetInput) => apiClient.post<Budget>(`${BASE}/budgets`, input),
  /** Edit a budget's cap; carryOver (monthly rows) toggles rollover on the
   *  series — undefined leaves the stored flag untouched. */
  updateAmount: (id: number, amountCents: number, carryOver?: boolean) =>
    apiClient.put<Budget>(`${BASE}/budgets/${id}`, {
      amount_cents: amountCents,
      carry_over: carryOver,
    }),
  /** Mark an envelope finished (closed) or reopen it. */
  setStatus: (id: number, status: BudgetLifecycle) =>
    apiClient.put<Budget>(`${BASE}/budgets/${id}/status`, { status }),
  remove: (id: number) => apiClient.delete(`${BASE}/budgets/${id}`),
};