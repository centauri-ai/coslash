// @vitest-environment jsdom
import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiFetch } from './api';
import { canFallbackSearch, searchHitKey, searchParts, useRankedSearch } from './search';

vi.mock('./api', () => ({ apiFetch: vi.fn() }));

const fetch = vi.mocked(apiFetch);
let root: Root | undefined;
let current: ReturnType<typeof useRankedSearch>;
function Probe({ query }: { query: string }) {
  const state = useRankedSearch(query);
  useEffect(() => {
    current = state;
  }, [state]);
  return null;
}
async function render(query: string) {
  if (!root) {
    root = createRoot(document.createElement('div'));
  }
  await act(async () => root!.render(<Probe query={query} />));
}
async function tick(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}
function response(body: unknown) {
  return { ok: true, json: async () => body } as Response;
}
const results = { indexState: 'ready', sessions: [], decisions: [], truncated: false };
afterEach(async () => {
  if (root) await act(async () => root!.unmount());
  root = undefined;
  vi.useRealTimers();
  vi.resetAllMocks();
});

describe('ranked search', () => {
  it('separates all prefixes and retains quoted content', () => {
    expect(
      searchParts('repo:app agent:codex machine:box group:work status:idle since:123 "merge queue"'),
    ).toEqual({
      content: '"merge queue"',
      filters: 'repo:app agent:codex machine:box group:work status:idle since:123',
    });
    expect(searchParts('repo:app').content).toBe('');
    expect(searchParts('* ""').content).toBe('');
    expect(searchParts('repo:app *').content).toBe('');
    expect(canFallbackSearch('merge queue')).toBe(true);
    expect(canFallbackSearch('"merge queue"')).toBe(false);
    expect(canFallbackSearch('since:123 queue')).toBe(false);
    expect(searchHitKey({ sourceId: 'local', agent: 'codex', sessionId: 'one' })).not.toBe(
      searchHitKey({ sourceId: 'ssh', agent: 'codex', sessionId: 'one' }),
    );
  });
  it('polls building and runs the current query once ready without an edit', async () => {
    vi.useFakeTimers();
    fetch
      .mockResolvedValueOnce(response({ state: 'building' }))
      .mockResolvedValueOnce(response({ state: 'ready' }))
      .mockResolvedValueOnce(response(results));
    await render('merge queue');
    await tick(250);
    expect(current.state).toBe('building');
    await tick(5000);
    expect(current.results).toEqual(results);
    expect(fetch.mock.calls[2][0]).toContain('q=merge%20queue');
  });
  it('ignores a stale response and cancels polling on clear', async () => {
    vi.useFakeTimers();
    let resolve!: (response: Response) => void;
    fetch.mockResolvedValueOnce(response({ state: 'ready' })).mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolve = r;
        }),
    );
    await render('old');
    await tick(250);
    fetch.mockResolvedValueOnce(response({ state: 'ready' })).mockResolvedValueOnce(response(results));
    await render('new');
    await tick(250);
    await act(async () => resolve(response({ ...results, truncated: true })));
    expect(current.results?.truncated).toBe(false);
    fetch.mockResolvedValueOnce(response({ state: 'building' }));
    await render('');
    await tick(0);
    const calls = fetch.mock.calls.length;
    await tick(15000);
    expect(fetch).toHaveBeenCalledTimes(calls);
  });
  it('reports failures and retries the search', async () => {
    vi.useFakeTimers();
    fetch
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(response({ state: 'ready' }))
      .mockResolvedValueOnce(response(results));
    await render('queue');
    await tick(250);
    expect(current.state).toBe('unavailable');
    await tick(5000);
    expect(current.results).toEqual(results);
  });
});
