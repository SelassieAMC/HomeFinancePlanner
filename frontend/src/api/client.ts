// Thin typed fetch wrapper for the backend API. All network access goes
// through this module (or modules built on it) — never fetch() in components.

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

interface ErrorEnvelope {
  error: string;
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // JSON bodies declare their type; FormData bodies must not — the browser
  // sets multipart/form-data with the boundary itself.
  const headers =
    init?.body instanceof FormData
      ? undefined
      : init?.body
        ? { 'Content-Type': 'application/json' }
        : undefined;

  const res = await fetch(path, {
    ...init,
    headers,
  });

  if (!res.ok) {
    let message = `request failed with status ${res.status}`;
    try {
      const body = (await res.json()) as ErrorEnvelope;
      if (body?.error) message = body.error;
    } catch {
      // keep the generic message
    }
    throw new ApiError(res.status, message);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export const apiClient = {
  get<T>(path: string, init?: { signal?: AbortSignal }): Promise<T> {
    return request<T>(path, init);
  },
  post<T>(path: string, body: unknown): Promise<T> {
    return request<T>(path, { method: 'POST', body: JSON.stringify(body) });
  },
  postForm<T>(path: string, form: FormData): Promise<T> {
    // No Content-Type header — the browser sets the multipart boundary.
    return request<T>(path, { method: 'POST', body: form });
  },
  put<T>(path: string, body: unknown): Promise<T> {
    return request<T>(path, { method: 'PUT', body: JSON.stringify(body) });
  },
  delete(path: string): Promise<void> {
    return request<void>(path, { method: 'DELETE' });
  },
};