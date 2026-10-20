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
