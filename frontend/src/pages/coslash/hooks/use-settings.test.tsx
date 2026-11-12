// @vitest-environment jsdom
import { act, useEffect } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { apiFetch } from '@/pages/coslash/lib/api';
import type { SettingsResponse } from '@/pages/coslash/lib/settings';
import { useSettings } from './use-settings';

vi.mock('@/pages/coslash/lib/api', () => ({ apiFetch: vi.fn() }));
let root: Root;
let state: ReturnType<typeof useSettings>;
function settings(refreshing: boolean): SettingsResponse {
  return {
    settings: {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: false, backend: 'claude-cli', model: 'claude-haiku-4-5' },
      appearance: { theme: 'light' },
      launch: { terminal: 'Terminal' },
    },
    persisted: true,
    valid: true,
    options: {
      refreshing,
      synthesisBackends: [],
      terminals: [],
      reviewers: refreshing ? [] : [{ id: 'codex', label: 'Codex', available: true }],
    },
  };
}
function response(data: SettingsResponse) {
  return new Response(JSON.stringify(data));
}
function deferred() {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
async function mount() {
  function Harness() {
    const value = useSettings();
    useEffect(() => {
      state = value;
    });
    return null;
  }
  await act(async () => root.render(<Harness />));
}
async function poll() {
  await act(async () => vi.advanceTimersByTimeAsync(1000));
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  root = createRoot(document.createElement('div'));
  vi.mocked(apiFetch).mockReset();
});
afterEach(async () => {
  await act(async () => root.unmount());
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('returns settings immediately and polls only until capabilities are ready', async () => {
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(true)))
    .mockResolvedValueOnce(response(settings(false)));
  await mount();
  expect(state.isLoading).toBe(false);
  expect(state.response?.options.refreshing).toBe(true);
  await poll();
  expect(state.response?.options.reviewers[0]?.id).toBe('codex');
  await poll();
  expect(apiFetch).toHaveBeenCalledTimes(2);
});

it('retries a failed capability refresh without discarding usable settings', async () => {
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(true)))
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValueOnce(response(settings(false)));
  await mount();
  await poll();
  expect(state.loadError).toBeNull();
  expect(state.response?.valid).toBe(true);
  await poll();
  expect(state.response?.options.refreshing).toBe(false);
});

it('does not let an older discovery GET overwrite a completed save', async () => {
  const pending = deferred();
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(true)))
    .mockReturnValueOnce(pending.promise);
  await mount();
  await poll();
  const saved = settings(true);
  saved.settings.appearance.theme = 'dark';
  vi.mocked(apiFetch).mockResolvedValueOnce(response(saved));
  await act(async () => {
    expect(await state.save(saved.settings)).toBe(true);
  });
  await act(async () => pending.resolve(response(settings(false))));
  expect(state.response?.settings.appearance.theme).toBe('dark');
  expect(state.response?.options.refreshing).toBe(false);
});

it('aborts and ignores an in-flight poll when unmounted', async () => {
  const pending = deferred();
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(true)))
    .mockReturnValueOnce(pending.promise);
  await mount();
  await poll();
  const options = vi.mocked(apiFetch).mock.calls[1][1];
  await act(async () => root.unmount());
  expect(options?.signal?.aborted).toBe(true);
  await act(async () => pending.resolve(response(settings(false))));
  await poll();
  expect(apiFetch).toHaveBeenCalledTimes(2);
  root = createRoot(document.createElement('div'));
});

it('runs overlapping saves one at a time and stays saving until both end', async () => {
  const first = deferred();
  const second = deferred();
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(false)))
    .mockReturnValueOnce(first.promise)
    .mockReturnValueOnce(second.promise);
  await mount();
  const base = settings(false).settings;
  let saves: Promise<boolean>[] = [];
  await act(async () => {
    saves = [state.save(base), state.save(base)];
  });
  expect(apiFetch).toHaveBeenCalledTimes(2);
  expect(state.isSaving).toBe(true);
  await act(async () => first.resolve(response(settings(false))));
  expect(apiFetch).toHaveBeenCalledTimes(3);
  expect(state.isSaving).toBe(true);
  await act(async () => second.resolve(response(settings(false))));
  expect(state.isSaving).toBe(false);
  await expect(Promise.all(saves)).resolves.toEqual([true, true]);
});

it('gives a queued updater the response of the save before it', async () => {
  const enabled = settings(false);
  enabled.settings.synthesis.enabled = true;
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(false)))
    .mockResolvedValueOnce(response(enabled))
    .mockResolvedValueOnce(response(enabled));
  await mount();
  await act(async () => {
    void state.save(enabled.settings);
    await state.save((current) => ({ ...current.settings, appearance: { theme: 'dark' } }));
  });
  const body = JSON.parse(vi.mocked(apiFetch).mock.calls[2][1]?.body as string);
  expect(body.settings.synthesis.enabled).toBe(true);
  expect(body.settings.appearance.theme).toBe('dark');
});

it('reports a save error only for the newest save', async () => {
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(response(settings(false)))
    .mockResolvedValueOnce(new Response('disk full', { status: 500 }))
    .mockResolvedValueOnce(response(settings(false)));
  await mount();
  const base = settings(false).settings;
  await act(async () => {
    expect(await Promise.all([state.save(base), state.save(base)])).toEqual([false, true]);
  });
  expect(state.saveError).toBeNull();
  expect(state.isSaving).toBe(false);
});
