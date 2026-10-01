import { expect, it, vi } from 'vitest';
import { apiFetch } from '@/pages/coslash/lib/api';
import { currentSynthesisCostsState, loadSynthesisCosts } from './use-synthesis-costs';

vi.mock('@/pages/coslash/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/pages/coslash/lib/api')>()),
  apiFetch: vi.fn(),
}));

it('rejects HTTP and malformed responses instead of treating them as zero spend', async () => {
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response('{}', { status: 503 }));
  await expect(loadSynthesisCosts({ since: 1, until: 2 }, new AbortController().signal)).rejects.toThrow(
    '503',
  );
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response('{}'));
  await expect(loadSynthesisCosts({ since: 1, until: 2 }, new AbortController().signal)).rejects.toThrow(
    'Invalid synthesis costs response',
  );
});

it('passes abort signals to the request so a superseded month can be cancelled', async () => {
  const first = new AbortController();
  vi.mocked(apiFetch).mockImplementationOnce((_path, init) => {
    expect(init?.signal).toBe(first.signal);
    return new Promise((_resolve, reject) => {
      first.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    });
  });
  const stale = loadSynthesisCosts({ since: 1, until: 2 }, first.signal);
  first.abort();
  await expect(stale).rejects.toMatchObject({ name: 'AbortError' });
});

it('discards a late response even if transport ignores the abort', async () => {
  const controller = new AbortController();
  let finish!: (response: Response) => void;
  vi.mocked(apiFetch).mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        finish = resolve;
      }),
  );
  const stale = loadSynthesisCosts({ since: 1, until: 2 }, controller.signal);
  controller.abort();
  finish(new Response(JSON.stringify({ sourceId: 'local' })));
  await expect(stale).rejects.toMatchObject({ name: 'AbortError' });
});

it('reads a sanitized month response with an explicit zero amount', async () => {
  const fixture = {
    sourceId: 'local',
    trackingStartedAtMs: 1,
    historicalUnknown: false,
    totals: {
      knownCostMicroUsd: 0,
      roundCount: 0,
      invocationCount: 0,
      unknownInvocationCount: 0,
      incompleteRoundCount: 0,
    },
    byVendor: [],
    rounds: [],
    nextCursor: null,
  };
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response(JSON.stringify(fixture)));
  await expect(loadSynthesisCosts({ since: 10, until: 20 }, new AbortController().signal)).resolves.toEqual(
    fixture,
  );
  expect(vi.mocked(apiFetch).mock.lastCall?.[0]).toBe('/api/synthesis-costs?source=local&since=10&until=20');
});

it('hides stale month and retry results until the current attempt completes', () => {
  const loaded = { key: 'month:1:2', attempt: 0, data: { sourceId: 'local' }, error: null };
  expect(currentSynthesisCostsState(loaded, 'month:1:2', 0)).toBe(loaded);
  expect(currentSynthesisCostsState(loaded, 'month:2:3', 0)).toBeNull();
  expect(currentSynthesisCostsState(loaded, 'month:1:2', 1)).toBeNull();
});
