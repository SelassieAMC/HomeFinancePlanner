import { apiClient } from './client';
import type { User, UserInput } from '../types/domain';

const BASE = '/api/v1/users';

// Admin-only user management (the backend rejects calls from plain members
// with 403; the UI hides the section behind the same flag from
// authApi.me().is_admin).
export const usersApi = {
  list: () => apiClient.get<User[]>(BASE),
  create: (input: UserInput) => apiClient.post<User>(BASE, input),
};