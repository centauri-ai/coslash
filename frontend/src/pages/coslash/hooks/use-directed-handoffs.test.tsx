// @vitest-environment jsdom
import { act, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import { apiFetch } from '@/pages/coslash/lib/api';
import { useDirectedHandoffs } from './use-directed-handoffs';

vi.mock('@/pages/coslash/lib/api', () => ({ apiFetch: vi.fn() }));

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('skips a poll while the previous handoffs request is still running', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  let resolve!: (response: Response) => void;
  vi.mocked(apiFetch).mockReturnValueOnce(new Promise((done) => (resolve = done)));
  let state!: ReturnType<typeof useDirectedHandoffs>;
  function Harness() {
    const value = useDirectedHandoffs();
    useEffect(() => {
      state = value;
    });
    return null;
  }
  const root = createRoot(document.createElement('div'));
  await act(async () => root.render(<Harness />));
  await act(async () => vi.advanceTimersByTimeAsync(6000));
  expect(apiFetch).toHaveBeenCalledTimes(1);

  await act(async () => resolve(new Response(JSON.stringify({ handoffs: [{ id: 'h1' }] }))));
  expect(state.handoffs).toEqual([{ id: 'h1' }]);

  vi.mocked(apiFetch).mockResolvedValue(new Response(JSON.stringify({ handoffs: [] })));
  await act(async () => vi.advanceTimersByTimeAsync(3000));
  expect(apiFetch).toHaveBeenCalledTimes(2);
  await act(async () => root.unmount());
});

it('times out a stuck handoffs request so polling continues', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.mocked(apiFetch).mockImplementationOnce(
    (_path, init) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(init.signal?.reason));
      }),
  );
  let state!: ReturnType<typeof useDirectedHandoffs>;
  function Harness() {
    const value = useDirectedHandoffs();
    useEffect(() => {
      state = value;
    });
    return null;
  }
  const root = createRoot(document.createElement('div'));
  await act(async () => root.render(<Harness />));
  vi.mocked(apiFetch).mockResolvedValue(new Response(JSON.stringify({ handoffs: [] })));
  await act(async () => vi.advanceTimersByTimeAsync(10_000));
  expect(state.error).toBe('Could not load handoffs (timed out)');

  await act(async () => vi.advanceTimersByTimeAsync(3000));
  expect(apiFetch).toHaveBeenCalledTimes(2);
  await act(async () => root.unmount());
});
