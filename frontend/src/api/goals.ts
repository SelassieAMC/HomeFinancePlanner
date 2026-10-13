import { apiClient } from './client';
import type { GoalStatus, GoalInput } from '../types/domain';

const BASE = '/api/v1';

export const goalsApi = {
  list: () => apiClient.get<GoalStatus[]>(`${BASE}/goals`),
  get: (id: number) => apiClient.get<GoalStatus>(`${BASE}/goals/${id}`),
  create: (input: GoalInput) => apiClient.post<GoalStatus>(`${BASE}/goals`, input),
  update: (id: number, input: GoalInput) =>
    apiClient.put<GoalStatus>(`${BASE}/goals/${id}`, input),
  remove: (id: number) => apiClient.delete(`${BASE}/goals/${id}`),
};