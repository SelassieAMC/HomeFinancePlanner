import { apiClient } from './client';
import type { LoginUser, User } from '../types/domain';

const BASE = '/api/v1';

// Login avatar URLs are bustable caches: the photo file is random-named per
// upload, but the browser caches the URL — bump `v` when the user changes.
export function avatarUrl(userId: number): string {
  return `${BASE}/users/${userId}/photo`;
}

export const authApi = {
  // Public: the identities the login dropdown offers (no roles included).
  loginUsers: () => apiClient.get<LoginUser[]>(`${BASE}/auth/login-users`),
  login: (userId: number, password: string, keepLoggedIn: boolean) =>
    apiClient.post<User>(`${BASE}/auth/login`, {
      user_id: userId,
      password,
      keep_logged_in: keepLoggedIn,
    }),
  logout: () => apiClient.post<void>(`${BASE}/auth/logout`, {}),
  me: () => apiClient.get<User>(`${BASE}/auth/me`),
  updateFullName: (fullName: string) =>
    apiClient.put<User>(`${BASE}/me`, { full_name: fullName }),
  changePassword: (currentPassword: string, newPassword: string) =>
    apiClient.put<void>(`${BASE}/me/password`, {
      current_password: currentPassword,
      new_password: newPassword,
    }),
  uploadPhoto: (file: File) => {
    const form = new FormData();
    form.append('photo', file);
    return apiClient.postForm<User>(`${BASE}/me/photo`, form);
  },
  removePhoto: () => apiClient.delete<User>(`${BASE}/me/photo`),
};