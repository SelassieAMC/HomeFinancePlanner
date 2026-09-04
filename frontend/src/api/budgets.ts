import { apiClient } from './client';
import type { Budget, BudgetInput } from '../types/domain';

const BASE = '/api/v1';

export const budgetsApi = {
  listByMonth: (month: string) =>
    apiClient.get<Budget[]>(`${BASE}/budgets?month=${encodeURIComponent(month)}`),
  create: (input: BudgetInput) => apiClient.post<Budget>(`${BASE}/budgets`, input),
  updateAmount: (id: number, amountCents: number) =>
    apiClient.put<Budget>(`${BASE}/budgets/${id}`, { amount_cents: amountCents }),
  remove: (id: number) => apiClient.delete(`${BASE}/budgets/${id}`),
};