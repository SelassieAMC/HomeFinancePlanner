import { apiClient } from './client';
import type { Category } from '../types/domain';

const BASE = '/api/v1';

export const categoriesApi = {
  list: () => apiClient.get<Category[]>(`${BASE}/categories`),
  create: (name: string) =>
    apiClient.post<Category>(`${BASE}/categories`, { name }),
  remove: (id: number) => apiClient.delete(`${BASE}/categories/${id}`),
};