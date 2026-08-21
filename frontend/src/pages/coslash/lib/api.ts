export const API_AUTHENTICATION_MESSAGE = 'This link expired — reopen the URL printed in your terminal.';

const TOKEN_STORAGE_KEY = 'coslash-api-token';

export class ApiAuthenticationError extends Error {
  constructor() {
    super(API_AUTHENTICATION_MESSAGE);
    this.name = 'ApiAuthenticationError';
  }
}

export type ApiErrorBody = {
  code: string;
  error: string;
};

export function decodeApiError(value: unknown): ApiErrorBody {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid API error');
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.code !== 'string' || typeof raw.error !== 'string') {
    throw new Error('Invalid API error');
  }
  return { code: raw.code, error: raw.error };
}

export async function readApiError(response: Response): Promise<ApiErrorBody | null> {
  const text = (await response.text()).trim();
  if (text === '') return null;
  try {
    return decodeApiError(JSON.parse(text) as unknown);
  } catch {
    return null;
  }
}

function loadToken(): string | null {
  const fragment = new URLSearchParams(window.location.hash.slice(1));
  const suppliedToken = fragment.get('t');
  if (suppliedToken != null && suppliedToken !== '') {
    window.sessionStorage.setItem(TOKEN_STORAGE_KEY, suppliedToken);
    window.history.replaceState(
      window.history.state,
      '',
      `${window.location.pathname}${window.location.search}`,
    );
    return suppliedToken;
  }
  return window.sessionStorage.getItem(TOKEN_STORAGE_KEY);
}

export async function apiFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const token = loadToken();
  const headers = new Headers(init.headers);
  if (token != null) headers.set('X-Coslash-Token', token);
  const response = await fetch(path, { ...init, headers });
  if (response.status === 401) throw new ApiAuthenticationError();
  return response;
}
