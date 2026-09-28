import { apiClient } from './client';
import type { AIPromptCreateInput, AIPromptInput, AIPromptView } from '../types/domain';

const BASE = '/api/v1/prompts';

export const promptsApi = {
  list: () => apiClient.get<AIPromptView[]>(BASE),
  get: (id: number) => apiClient.get<AIPromptView>(`${BASE}/${id}`),
  create: (input: AIPromptCreateInput) => apiClient.post<AIPromptView>(BASE, input),
  /** Replaces name/description/content; the key is immutable. */
  update: (id: number, input: AIPromptInput) =>
    apiClient.put<AIPromptView>(`${BASE}/${id}`, input),
  /** Safe even for the seeded prompts: processes fall back to the built-in default. */
  remove: (id: number) => apiClient.delete(`${BASE}/${id}`),
};