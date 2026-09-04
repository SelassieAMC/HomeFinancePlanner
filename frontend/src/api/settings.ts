import { apiClient } from './client';
import type { AIProvider, AIProviderType } from '../types/domain';

const BASE = '/api/v1/settings';

export interface AIProviderInput {
  id?: string;
  type: AIProviderType;
  base_url?: string;
  api_key?: string; // empty keeps the stored key
  model: string;
}

export interface ConnectionTestResult {
  ok: boolean;
  error?: string;
}

export const settingsApi = {
  listAIProviders: () => apiClient.get<AIProvider[]>(`${BASE}/ai`),
  saveAIProviders: (providers: AIProviderInput[]) =>
    apiClient.put<AIProvider[]>(`${BASE}/ai`, providers),
  testAIProvider: (id: string) =>
    apiClient.post<ConnectionTestResult>(`${BASE}/ai/test/${encodeURIComponent(id)}`, {}),
};