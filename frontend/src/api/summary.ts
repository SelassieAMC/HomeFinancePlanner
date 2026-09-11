import { apiClient } from './client';
import type { Summary } from '../types/domain';
import { monthBounds } from '../lib/date';

const BASE = '/api/v1';

export const summaryApi = {
  /** Dashboard aggregate for an inclusive date range (YYYY-MM-DD). */
  range: (from: string, to: string) =>
    apiClient.get<Summary>(
      `${BASE}/summary?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    ),
  /** Dashboard aggregate for one calendar month (YYYY-MM). */
  month: (month: string) => {
    const { from, to } = monthBounds(month);
    return summaryApi.range(from, to);
  },
};