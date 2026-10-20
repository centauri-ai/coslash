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

it('keeps first-run settings usable when discovery remains in progress', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const onSave = vi.fn(async () => true);

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
      synthesis: expect.objectContaining({
        enabled: false,
      }),
      syncPaused: true,
    }),
    undefined,
  );

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={settingsResponse(false, true)} />),
  );
  expect(onSave).toHaveBeenLastCalledWith(
    expect.objectContaining({
      synthesis: expect.objectContaining({
        backend: 'codex_exec',
        model: 'gpt-6-luna',
      }),
    }),
    undefined,
  );

  await act(async () => root.unmount());
  mount.remove();
});

it('keeps a first-run backend or model choice made before discovery completes', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const onSave = vi.fn(async () => true);
  const refreshingResponse = settingsResponse(true);
  refreshingResponse.options.synthesisBackends = [
    {
      id: 'codex_exec',
      label: 'Codex CLI',
      models: [
        { id: 'gpt-6-luna', label: 'GPT-6 Luna', default: true },
        { id: 'manual-model', label: 'Manual model', default: false },
      ],
      available: true,
    },
    {
      id: 'claude-cli',
      label: 'Claude Code CLI',
      models: [{ id: 'claude-haiku-4-5', label: 'Claude Haiku', default: true }],
      available: true,
    },
  ];

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={refreshingResponse} />),
  );
  const synthesisSwitch = document.querySelector<HTMLButtonElement>('[aria-label="AI synthesis"]');
  await act(async () => synthesisSwitch?.click());
  const modelSelect = document.querySelector<HTMLSelectElement>('[aria-label="Synthesis model"]');
  await act(async () => {
    if (!modelSelect) return;
    modelSelect.value = 'manual-model';
    modelSelect.dispatchEvent(new Event('change', { bubbles: true }));
  });

  await act(async () =>
    root.render(<SettingsDialog {...dialogProps} onSave={onSave} response={settingsResponse(false, true)} />),
  );
  expect(document.querySelector<HTMLSelectElement>('[aria-label="Synthesis model"]')?.value).toBe(
    'manual-model',
  );

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
