import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiAuthenticationError, apiFetch } from './api';

describe('apiFetch', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses a new token fragment that arrives after the module is loaded', async () => {
    const stored = new Map<string, string>();
    const location = { hash: '#t=old-token', pathname: '/', search: '' };
    const replaceState = vi.fn(() => {
      location.hash = '';
    });
    vi.stubGlobal('window', {
      location,
      history: { state: null, replaceState },
      sessionStorage: {
        getItem: (key: string) => stored.get(key) ?? null,
        setItem: (key: string, value: string) => stored.set(key, value),
      },
    });

    const fetchMock = vi.fn().mockResolvedValue(new Response(null));
    vi.stubGlobal('fetch', fetchMock);

    await apiFetch('/api/sessions');
    location.hash = '#t=new-token';
    await apiFetch('/api/sessions');

    const secondRequest = fetchMock.mock.calls[1]?.[1] as RequestInit;
    expect(new Headers(secondRequest.headers).get('X-Coslash-Token')).toBe('new-token');
    expect(stored.get('coslash-api-token')).toBe('new-token');
    expect(replaceState).toHaveBeenCalledTimes(2);
  });

  it('maps a 401 response to ApiAuthenticationError', async () => {
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })));

    await expect(apiFetch('/api/sessions')).rejects.toBeInstanceOf(ApiAuthenticationError);
  });
});
