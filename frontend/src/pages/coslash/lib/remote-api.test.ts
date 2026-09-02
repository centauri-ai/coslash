import { afterEach, describe, expect, it, vi } from 'vitest';
import { retryRemoteRefreshAndWait } from './remote-api';

const connectingMachine = {
  sourceId: 'r_0123456789abcdef',
  label: 'gpu-server',
  state: 'connecting',
  complete: false,
};

const readyMachine = { ...connectingMachine, state: 'ok', complete: true };

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
});
