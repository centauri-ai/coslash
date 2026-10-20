// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it } from 'vitest';
import { SettingsDialog } from '@/pages/coslash/components/SettingsDialog';
import { type SettingsResponse } from '@/pages/coslash/lib/settings';

function settingsResponse(refreshing: boolean): SettingsResponse {
  return {
    settings: {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: false, backend: 'claude-cli', model: 'claude-haiku-4-5' },
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

it('waits for first-run discovery before initializing the settings draft', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);

  await act(async () => root.render(<SettingsDialog {...dialogProps} response={settingsResponse(true)} />));
  expect(document.body.textContent).toContain('Checking installed agents...');
  expect(document.querySelector('[aria-label="AI synthesis"]')).toBeNull();

  await act(async () => root.render(<SettingsDialog {...dialogProps} response={settingsResponse(false)} />));
  const synthesisSwitch = document.querySelector<HTMLButtonElement>('[aria-label="AI synthesis"]');
  expect(synthesisSwitch).not.toBeNull();
  await act(async () => synthesisSwitch?.click());
  const codex = Array.from(document.querySelectorAll('button[aria-pressed]')).find((button) =>
    button.textContent?.includes('Codex'),
  );
  expect(codex?.getAttribute('aria-pressed')).toBe('true');

  await act(async () => root.unmount());
  mount.remove();
});
