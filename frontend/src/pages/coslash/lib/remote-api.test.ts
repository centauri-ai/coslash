import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  remoteAuthenticationStatus,
  retryRemoteRefreshAndWait,
  startRemoteAuthentication,
} from './remote-api';

const connectingMachine = {
  sourceId: 'r_0123456789abcdef',
  label: 'gpu-server',
  state: 'connecting',
  complete: false,
};

const readyMachine = { ...connectingMachine, state: 'ok', complete: true };
const refreshingMachine = { ...readyMachine, refreshing: true };

describe('retryRemoteRefreshAndWait', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('waits for collection to leave connecting before resolving', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json(connectingMachine, { status: 202 }))
      .mockResolvedValueOnce(Response.json(readyMachine));
    vi.stubGlobal('fetch', fetchMock);

    const result = retryRemoteRefreshAndWait();
    await vi.advanceTimersByTimeAsync(400);

    await expect(result).resolves.toEqual(readyMachine);
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/remote/retry');
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/api/remote/status');
  });

  it('waits for a refresh even when cached health remains ok', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json(refreshingMachine, { status: 202 }))
      .mockResolvedValueOnce(Response.json(refreshingMachine))
      .mockResolvedValueOnce(Response.json(readyMachine));
    vi.stubGlobal('fetch', fetchMock);

    const result = retryRemoteRefreshAndWait();
    await vi.advanceTimersByTimeAsync(800);

    await expect(result).resolves.toEqual(readyMachine);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});

describe('terminal authentication API', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('starts and decodes an opaque authentication attempt', async () => {
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    const fetchMock = vi.fn().mockResolvedValue(Response.json({ id: 'a'.repeat(32), state: 'waiting' }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(startRemoteAuthentication('jane@host')).resolves.toEqual({
      id: 'a'.repeat(32),
      state: 'waiting',
    });
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/remote/auth/start');
  });

  it('rejects unknown authentication states', async () => {
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ id: 'a'.repeat(32), state: 'leaked' })));

    await expect(remoteAuthenticationStatus('a'.repeat(32))).rejects.toThrow('Invalid authentication status');
  });
});
