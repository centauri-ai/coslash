// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { SettingsDialog } from '@/pages/coslash/components/SettingsDialog';
import { type SettingsResponse } from '@/pages/coslash/lib/settings';

function settingsResponse(refreshing: boolean): SettingsResponse {
  return {
    settings: {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: true, backend: 'claude-cli', model: 'claude-haiku-4-5' },
      appearance: { theme: 'light' },
      launch: { terminal: 'Terminal' },
    },
    persisted: false,
    valid: true,
    options: {
      refreshing,
      synthesisBackends: [
        { id: 'claude-cli', label: 'Claude Code CLI', models: [], available: false },
        {
          id: 'codex_exec',
          label: 'Codex CLI',
          models: [{ id: 'gpt-6-luna', label: 'GPT-6 Luna', default: true }],
          available: !refreshing,
        },
      ],
      terminals: [],
      reviewers: [],
    },
  };
}

const dialogProps: Omit<ComponentProps<typeof SettingsDialog>, 'response'> = {
  open: true,
  mode: 'full-settings',
  onOpenChange: () => {},
  isLoading: false,
  loadError: null,
  saveError: null,
  isSaving: false,
  onSave: async () => false,
  syncStatusLoadState: 'ready',
};

it('keeps first-run settings usable during discovery with synthesis disabled', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const onSave = vi.fn(async () => false);

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={settingsResponse(true)} />),
  );
  expect(document.body.textContent).toContain('Checking installed agents...');
  const synthesisSwitch = document.querySelector<HTMLButtonElement>('[aria-label="AI synthesis"]');
  expect(synthesisSwitch?.getAttribute('aria-checked')).toBe('false');
  const pauseSwitch = document.querySelector<HTMLButtonElement>(
    '[aria-label="Pause syncing on this computer"]',
  );
  expect(pauseSwitch).not.toBeNull();
  await act(async () => pauseSwitch?.click());
  expect(onSave).toHaveBeenCalledWith(
    expect.objectContaining({
      synthesis: expect.objectContaining({ enabled: false }),
      syncPaused: true,
    }),
    undefined,
  );

  await act(async () => root.render(<SettingsDialog {...dialogProps} response={settingsResponse(false)} />));
  const resolvedSynthesisSwitch = document.querySelector<HTMLButtonElement>('[aria-label="AI synthesis"]');
  await act(async () => resolvedSynthesisSwitch?.click());
  const codex = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')).find(
    (button) => button.textContent?.includes('Codex'),
  );
  await act(async () => codex?.click());
  expect(codex?.getAttribute('aria-pressed')).toBe('true');

  await act(async () => root.unmount());
  mount.remove();
});
