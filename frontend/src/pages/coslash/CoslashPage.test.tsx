// @vitest-environment jsdom
import { act, type ComponentProps, type ReactElement } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import type { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import type { DiagnosticsDialog } from '@/pages/coslash/components/DiagnosticsDialog';
import type { SettingsDialog } from '@/pages/coslash/components/SettingsDialog';
import { useSettings } from '@/pages/coslash/hooks/use-settings';
import { retryRemoteRefreshAndWait } from '@/pages/coslash/lib/remote-api';
import type { CoslashSettings } from '@/pages/coslash/lib/settings';
import { CoslashPage } from './CoslashPage';

const { retrySessions, refreshDiagnostics } = vi.hoisted(() => ({
  retrySessions: vi.fn(),
  refreshDiagnostics: vi.fn(),
}));
let dialog: ComponentProps<typeof SettingsDialog>;
let layout: ComponentProps<typeof CoslashLayout>;
vi.mock('@/pages/coslash/components/CoslashLayout', () => ({
  CoslashLayout: (props: typeof layout) => {
    layout = props;
    return null;
  },
}));
vi.mock('@/pages/coslash/components/SettingsDialog', () => ({
  SettingsDialog: (props: typeof dialog) => {
    dialog = props;
    return null;
  },
}));
vi.mock('@/pages/coslash/hooks/use-settings', () => ({ useSettings: vi.fn() }));
vi.mock('@/pages/coslash/components/SessionInspector', () => ({ SessionInspector: () => null }));
vi.mock('@/pages/coslash/hooks/use-sessions', () => ({
  useSessions: () => ({ sessions: [], machines: [], isLoading: false, retrySessions }),
}));
vi.mock('@/pages/coslash/hooks/use-diagnostics', () => ({
  useDiagnostics: () => ({ refresh: refreshDiagnostics }),
}));
vi.mock('@/pages/coslash/hooks/use-directed-handoffs', () => ({
  useDirectedHandoffs: () => ({ handoffs: [] }),
}));
vi.mock('@/pages/coslash/features/sync/use-sync-status', () => ({
  useSyncStatus: () => ({ status: { state: 'not_connected', sessions: {} } }),
}));
vi.mock('@/pages/coslash/lib/api', () => ({
  apiFetch: vi.fn(async () => new Response(null, { status: 503 })),
}));
vi.mock('@/pages/coslash/lib/remote-api', () => ({ retryRemoteRefreshAndWait: vi.fn() }));

const settings: CoslashSettings = {
  $schema: 'x',
  version: 1,
  synthesis: { enabled: false, backend: 'claude-cli', model: 'claude-haiku-4-5' },
  appearance: { theme: 'light' },
  launch: { terminal: 'terminal' },
};
const remote = { id: 'r_0123456789abcdef', sshAlias: 'test-host', enabled: true };
const root = () => createRoot(document.createElement('div'));
let mounted: ReturnType<typeof root>;

afterEach(async () => {
  await act(async () => mounted.unmount());
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

it.each([
  ['no remote', undefined, undefined, 0],
  ['removed last remote', remote, null, 0],
  ['disabled remote', remote, { ...remote, enabled: false }, 0],
  ['newly configured remote', undefined, remote, 1],
  ['configured remote', remote, remote, 1],
] as const)(
  'refreshes saved %s settings without an unintended remote retry',
  async (_, previous, next, retries) => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    const save = vi.fn(async () => true);
    vi.mocked(useSettings).mockReturnValue({
      response: {
        settings: { ...settings, remote: previous },
        persisted: true,
        valid: true,
        options: { synthesisBackends: [], terminals: [], reviewers: [] },
      },
      save,
      isLoading: false,
      loadError: null,
      saveError: null,
      isSaving: false,
    });
    vi.mocked(retryRemoteRefreshAndWait).mockResolvedValue({
      sourceId: remote.id,
      label: 'test-host',
      state: 'ok',
      complete: true,
    });
    mounted = root();
    await act(async () => mounted.render(<CoslashPage />));
    await act(async () => layout.onSettings?.());
    await act(async () =>
      (layout.diagnostics as ReactElement<ComponentProps<typeof DiagnosticsDialog>>).props.onOpenChange(true),
    );
    const proposed = { ...settings, remote: next };
    const ownershipAction = next === null ? 'release' : undefined;
    await act(async () => expect(await dialog.onSave(proposed, ownershipAction)).toBe(true));
    expect(save).toHaveBeenCalledWith(proposed, ownershipAction);
    expect(retryRemoteRefreshAndWait).toHaveBeenCalledTimes(retries);
    expect(retrySessions).toHaveBeenCalledTimes(1);
    expect(refreshDiagnostics).toHaveBeenCalledTimes(1);
  },
);

it('does not refresh after a failed save and keeps explicit retry available', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.mocked(useSettings).mockReturnValue({
    response: null,
    save: vi.fn(async () => false),
    isLoading: false,
    loadError: null,
    saveError: null,
    isSaving: false,
  });
  vi.mocked(retryRemoteRefreshAndWait).mockRejectedValue(new Error('offline'));
  mounted = root();
  await act(async () => mounted.render(<CoslashPage />));
  await act(async () => expect(await dialog.onSave({ ...settings, remote })).toBe(false));
  expect(retryRemoteRefreshAndWait).not.toHaveBeenCalled();
  expect(retrySessions).not.toHaveBeenCalled();
  await act(async () => {
    await dialog.onRemoteRetry?.();
  });
  expect(retryRemoteRefreshAndWait).toHaveBeenCalledTimes(1);
  expect(retrySessions).toHaveBeenCalledTimes(1);
});
