import { apiClient } from './client';
import type { PayablesReport, ScheduledAction, ScheduledActionInput, ScheduleRunView, ScheduleRunStatus } from '../types/domain';

const BASE = '/api/v1';

export interface ScheduleRunFiltersParams {
  action_id?: number;
  status?: ScheduleRunStatus[]; // comma-joined on the wire
  due_from?: string;
  due_to?: string;
  limit?: number;
}

function runsQuery(p: ScheduleRunFiltersParams): string {
  const q = new URLSearchParams();
  if (p.action_id) q.set('action_id', String(p.action_id));
  if (p.status?.length) q.set('status', p.status.join(','));
  if (p.due_from) q.set('due_from', p.due_from);
  if (p.due_to) q.set('due_to', p.due_to);
  if (p.limit) q.set('limit', String(p.limit));
  const s = q.toString();
  return s ? `?${s}` : '';
}

export const schedulesApi = {
  list: () => apiClient.get<ScheduledAction[]>(`${BASE}/scheduled-actions`),
  // One month of the occurrences register (MoneyWiz-style month paging;
  // YYYY-MM, default = the server's current month). The viewed month
  // materializes on demand, so pending rows are always actionable.
  payables: (month?: string) =>
    apiClient.get<PayablesReport>(`${BASE}/scheduled-actions/payables${month ? `?month=${month}` : ''}`),
  create: (input: ScheduledActionInput) =>
    apiClient.post<ScheduledAction>(`${BASE}/scheduled-actions`, input),
  update: (id: number, input: ScheduledActionInput) =>
    apiClient.put<ScheduledAction>(`${BASE}/scheduled-actions/${id}`, input),
  archive: (id: number) =>
    apiClient.post<ScheduledAction>(`${BASE}/scheduled-actions/${id}/archive`, {}),
  remove: (id: number) => apiClient.delete(`${BASE}/scheduled-actions/${id}`),
  runs: (params: ScheduleRunFiltersParams = {}) =>
    apiClient.get<ScheduleRunView[]>(`${BASE}/scheduled-actions/runs${runsQuery(params)}`),
  applyRun: (runId: number) =>
    apiClient.post<ScheduleRunView>(`${BASE}/scheduled-actions/runs/${runId}/apply`, {}),
  dismissRun: (runId: number) =>
    apiClient.post<ScheduleRunView>(`${BASE}/scheduled-actions/runs/${runId}/dismiss`, {}),
  skipRun: (runId: number) =>
    apiClient.post<ScheduleRunView>(`${BASE}/scheduled-actions/runs/${runId}/skip`, {}),
};