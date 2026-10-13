import { apiClient } from './client';
import type {
  BagStatus,
  BagInput,
  BagAllocation,
  BagAllocationInput,
  BagRebalanceInput,
  BagRebalancePair,
  BagOverview,
  YearClosePreview,
} from '../types/domain';

const BASE = '/api/v1';

export const bagsApi = {
  overview: () => apiClient.get<BagOverview>(`${BASE}/bags`),
  get: (id: number) => apiClient.get<BagStatus>(`${BASE}/bags/${id}`),
  create: (input: BagInput) => apiClient.post<BagStatus>(`${BASE}/bags`, input),
  update: (id: number, input: BagInput) => apiClient.put<BagStatus>(`${BASE}/bags/${id}`, input),
  setStatus: (id: number, status: 'open' | 'closed') =>
    apiClient.put<BagStatus>(`${BASE}/bags/${id}/status`, { status }),
  remove: (id: number) => apiClient.delete(`${BASE}/bags/${id}`),
  allocations: (bagId: number) => apiClient.get<BagAllocation[]>(`${BASE}/bags/${bagId}/allocations`),
  allocate: (bagId: number, input: BagAllocationInput) =>
    apiClient.post<BagAllocation>(`${BASE}/bags/${bagId}/allocations`, input),
  rebalance: (input: BagRebalanceInput) =>
    apiClient.post<BagRebalancePair>(`${BASE}/bags/rebalance`, input),
  yearClose: (year: string) =>
    apiClient.get<YearClosePreview>(`${BASE}/bags/year-close?year=${encodeURIComponent(year)}`),
};