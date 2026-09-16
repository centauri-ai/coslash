import { afterEach, describe, expect, it, vi } from 'vitest';
import type { MachineFact } from './machines';
import { retryRemoteRefreshAndWait, waitForRemoteRefresh } from './remote-api';

const connectingMachine: MachineFact = {
  sourceId: 'r_0123456789abcdef',
  label: 'gpu-server',
  state: 'connecting',
  complete: false,
};

const readyMachine: MachineFact = { ...connectingMachine, state: 'ok', complete: true };
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

  it('forwards retry statuses while waiting', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(Response.json(refreshingMachine, { status: 202 }))
        .mockResolvedValueOnce(Response.json(readyMachine)),
    );
    const observed: MachineFact[] = [];

    const result = retryRemoteRefreshAndWait((machine) => observed.push(machine));
    await vi.advanceTimersByTimeAsync(400);

    await expect(result).resolves.toEqual(readyMachine);
    expect(observed).toEqual([refreshingMachine, readyMachine]);
  });

  it('reports each durable publication while polling', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('window', {
      location: { hash: '', pathname: '/', search: '' },
      history: { state: null, replaceState: vi.fn() },
      sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
    });
    const publicationA = { ...refreshingMachine, publicationId: 'publication-a' };
    const publicationB = { ...readyMachine, publicationId: 'publication-b' };
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(Response.json(publicationA))
        .mockResolvedValueOnce(Response.json(publicationB)),
    );
    const observed: string[] = [];

    const result = waitForRemoteRefresh(connectingMachine, undefined, (machine) => {
      if (machine.publicationId != null) observed.push(machine.publicationId);
    });
    await vi.advanceTimersByTimeAsync(800);

    await expect(result).resolves.toEqual(publicationB);
    expect(observed).toEqual(['publication-a', 'publication-b']);
  });
});
