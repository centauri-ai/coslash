import { describe, expect, it } from 'vitest';
import {
  availableSynthesisBackends,
  settingsForSave,
  type BackendOption,
  type CoslashSettings,
} from '@/pages/coslash/lib/settings';

function backend(id: string, available: boolean): BackendOption {
  return { id, label: id, models: [], available };
}

describe('availableSynthesisBackends', () => {
  it('excludes undetected backends', () => {
    const options = [backend('claude-cli', true), backend('codex_exec', false)];

    expect(availableSynthesisBackends(options).map(({ id }) => id)).toEqual(['claude-cli']);
  });
});

describe('settingsForSave', () => {
  it('assigns a stable-format ID to a new remote without mutating the draft', () => {
    const settings = {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: false, backend: '', model: '' },
      appearance: { theme: 'light' },
      launch: { terminal: 'Terminal' },
      remote: { sshAlias: 'gpu-server', enabled: true },
    } as CoslashSettings;

    const saved = settingsForSave(settings);

    expect(saved.remote?.id).toMatch(/^r_[0-9a-f]{16}$/);
    expect(settings.remote?.id).toBeUndefined();
    expect(settingsForSave(saved)).toBe(saved);
  });
});
