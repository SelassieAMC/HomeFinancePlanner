import { apiClient } from './client';
import type { ProductInsight } from '../types/domain';

const BASE = '/api/v1/insights';

export const insightsApi = {
  /** Saved insights, newest first. unseen=true is the dashboard's unread list. */
  list: (opts?: { unseen?: boolean; limit?: number }) => {
    const params = new URLSearchParams();
    if (opts?.unseen) params.set('unseen', 'true');
    if (opts?.limit !== undefined) params.set('limit', String(opts.limit));
    const qs = params.toString();
    return apiClient.get<ProductInsight[]>(qs ? `${BASE}?${qs}` : BASE);
  },
  /** Acknowledges one insight (removes it from the unread list). */
  dismiss: (id: number) => apiClient.post<void>(`${BASE}/${id}/dismiss`, {}),
};