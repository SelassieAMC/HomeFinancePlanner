import { apiClient } from './client';
import type { MonthSummary } from '../types/domain';

const BASE = '/api/v1';

export const summaryApi = {
  month: (month: string) =>
    apiClient.get<MonthSummary>(`${BASE}/summary?month=${encodeURIComponent(month)}`),
};