// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { SettingsDialog } from '@/pages/coslash/components/SettingsDialog';
import { type SettingsResponse } from '@/pages/coslash/lib/settings';

function settingsResponse(refreshing: boolean, persisted = false): SettingsResponse {
  return {
    settings: {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: true, backend: 'claude-cli', model: 'claude-haiku-4-5' },
      appearance: { theme: 'light' },
      launch: { terminal: 'Terminal' },
    },
    persisted,
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

it('waits for first-run discovery before initializing the settings draft', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const onSave = vi.fn(async () => false);

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={settingsResponse(true)} />),
  );
  expect(document.body.textContent).toContain('Checking installed agents...');
  expect(document.querySelector('[aria-label="AI synthesis"]')).toBeNull();
  expect(document.querySelector('[aria-label="Pause syncing on this computer"]')).toBeNull();
  expect(onSave).not.toHaveBeenCalled();

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={settingsResponse(false)} />),
  );
  const resolvedSynthesisSwitch = document.querySelector<HTMLButtonElement>('[aria-label="AI synthesis"]');
  expect(resolvedSynthesisSwitch?.getAttribute('aria-checked')).toBe('false');
  const pauseSwitch = document.querySelector<HTMLButtonElement>(
    '[aria-label="Pause syncing on this computer"]',
  );
  await act(async () => pauseSwitch?.click());
  expect(onSave).toHaveBeenCalledWith(
    expect.objectContaining({
      synthesis: expect.objectContaining({
        enabled: false,
        backend: 'codex_exec',
        model: 'gpt-6-luna',
      }),
      syncPaused: true,
    }),
    undefined,
  );

  await act(async () => resolvedSynthesisSwitch?.click());
  const codex = Array.from(document.querySelectorAll<HTMLButtonElement>('button[aria-pressed]')).find(
    (button) => button.textContent?.includes('Codex'),
  );
  await act(async () => codex?.click());
  expect(codex?.getAttribute('aria-pressed')).toBe('true');

  await act(async () => root.unmount());
  mount.remove();
});

it('keeps persisted settings available while discovery continues', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} response={settingsResponse(true, true)} />),
  );

  expect(document.querySelector('[aria-label="AI synthesis"]')).not.toBeNull();
  expect(document.body.textContent).toContain('Checking installed agents...');

  await act(async () => root.unmount());
  mount.remove();
});
