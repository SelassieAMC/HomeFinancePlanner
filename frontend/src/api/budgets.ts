import { apiClient } from './client';
import type { Budget, BudgetInput, BudgetLifecycle, BudgetTransfer } from '../types/domain';

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
  /** Move allowance between two open monthly rows of one month, or (no
   *  from_budget_id) seed the target row's carried balance. */
  transfer: (input: {
    from_budget_id?: number; // absent = carried-balance seed
    to_budget_id: number;
    amount_cents: number;
    note?: string;
  }) =>
    apiClient.post<{ from: Budget | null; to: Budget }>(
      `${BASE}/budgets/transfer`,
      input,
    ),
  /** The auditable transfer history, newest first. */
  transfers: () => apiClient.get<BudgetTransfer[]>(`${BASE}/budgets/transfers`),
};