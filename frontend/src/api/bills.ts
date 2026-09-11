import { apiClient } from './client';
import type {
  Bill,
  BillConfirmInput,
  BillScan,
  BillScanStatus,
  BillStatsResponse,
} from '../types/domain';

const BASE = '/api/v1';

export type BillStatsGroupBy = 'market' | 'month' | 'week' | 'item' | 'category';

export interface BillListFilters {
  month?: string;
  market?: string;
}

function toQuery(filters: BillListFilters): string {
  const params = new URLSearchParams();
  if (filters.month) params.set('month', filters.month);
  if (filters.market) params.set('market', filters.market);
  const qs = params.toString();
  return qs ? `?${qs}` : '';
}

export const billsApi = {
  /** Upload a receipt for AI extraction. Returns immediately with status
   *  "analyzing"; poll getScan until the draft is ready. */
  scan: (file: File, providerId?: string) => {
    const form = new FormData();
    form.append('image', file);
    if (providerId) form.append('provider_id', providerId);
    return apiClient.postForm<BillScan>(`${BASE}/bills/scan`, form);
  },
  /** Poll one scan's pipeline state (analyzing → done/failed). */
  getScan: (token: string) => apiClient.get<BillScan>(`${BASE}/bills/scan/${token}`),
  /** Recent scans (analysis in progress / failed) for the bills view. */
  listScans: (statuses?: BillScanStatus[]) => {
    const qs = statuses?.length ? `?status=${statuses.join(',')}` : '';
    return apiClient.get<BillScan[]>(`${BASE}/bills/scans${qs}`);
  },
  /** Re-run extraction on a finished/failed scan (returns to "analyzing"). */
  reextract: (token: string, providerId?: string) =>
    apiClient.post<BillScan>(`${BASE}/bills/scan/${token}/extract`, {
      provider_id: providerId || undefined,
    }),
  /** Persist the confirmed draft as an accepted bill (+ optional expense). */
  confirm: (token: string, input: BillConfirmInput) =>
    apiClient.post<Bill>(`${BASE}/bills/scan/${token}/confirm`, input),
  /** Drop an unconfirmed scan and its stored receipt file. */
  discardScan: (token: string) =>
    apiClient.delete(`${BASE}/bills/scan/${token}`),
  list: (filters: BillListFilters = {}) =>
    apiClient.get<Bill[]>(`${BASE}/bills${toQuery(filters)}`),
  get: (id: number) => apiClient.get<Bill>(`${BASE}/bills/${id}`),
  /** Apply corrections to a saved bill (same payload as confirm). */
  update: (id: number, input: BillConfirmInput) =>
    apiClient.put<Bill>(`${BASE}/bills/${id}`, input),
  /** Remove a confirmed bill for good: its lines, the linked expense
   *  transaction, and the stored receipt file. */
  remove: (id: number) => apiClient.delete(`${BASE}/bills/${id}`),
  stats: (groupBy: BillStatsGroupBy, month?: string) => {
    const params = new URLSearchParams({ group_by: groupBy });
    if (month) params.set('month', month);
    return apiClient.get<BillStatsResponse>(`${BASE}/bills/stats?${params.toString()}`);
  },
  /** Distinct brands already recorded on bill items (dropdown options). */
  brands: () => apiClient.get<string[]>(`${BASE}/bills/brands`),
};