import { describe, expect, it } from 'vitest';
import {
  availableSynthesisBackends,
  enableSynthesisDraft,
  settingsForSave,
  type BackendOption,
  type CoslashSettings,
  type SettingsResponse,
} from '@/pages/coslash/lib/settings';

function backend(id: string, available: boolean): BackendOption {
  return {
    id,
    label: `${id} CLI`,
    models: [{ id: `${id}-model`, label: `${id}-model`, default: true }],
    available,
  };
}

function draft(overrides: Partial<CoslashSettings['synthesis']> = {}): CoslashSettings {
  return {
    $schema: 'https://coslash.io/settings.schema.json',
    version: 1,
    synthesis: {
      enabled: false,
      backend: 'claude-cli',
      model: 'claude-cli-model',
      ...overrides,
    },
    appearance: { theme: 'light' },
    launch: { terminal: 'terminal' },
  };
}

function response(backends: BackendOption[]): SettingsResponse {
  return {
    settings: draft(),
    persisted: false,
    valid: true,
    options: { synthesisBackends: backends, terminals: [] },
  };
}

describe('availableSynthesisBackends', () => {
  it('excludes undetected backends', () => {
    const options = [backend('claude-cli', true), backend('codex_exec', false)];

    expect(availableSynthesisBackends(options).map(({ id }) => id)).toEqual(['claude-cli']);
  });
});

describe('enableSynthesisDraft', () => {
  it('returns null when no CLI is available', () => {
    const loaded = response([backend('claude-cli', false), backend('codex_exec', false)]);

    expect(enableSynthesisDraft(draft(), loaded)).toBeNull();
  });

  it('enables synthesis on the first available CLI', () => {
    const loaded = response([backend('claude-cli', false), backend('codex_exec', true)]);

    expect(enableSynthesisDraft(draft({ backend: 'claude-cli' }), loaded)).toEqual(
      draft({
        enabled: true,
        backend: 'codex_exec',
        model: 'codex_exec-model',
      }),
    );
  });
});

describe('settingsForSave', () => {
  it('turns synthesis off when no CLI is available', () => {
    const loaded = response([backend('claude-cli', false)]);

    expect(settingsForSave(draft({ enabled: true, backend: 'claude-cli' }), loaded).synthesis.enabled).toBe(
      false,
    );
  });

  it('rewrites an unavailable backend to the first detected CLI', () => {
    const loaded = response([backend('claude-cli', false), backend('codex_exec', true)]);

    expect(settingsForSave(draft({ enabled: true, backend: 'claude-cli' }), loaded).synthesis).toEqual({
      enabled: true,
      backend: 'codex_exec',
      model: 'codex_exec-model',
    });
  });

  it('leaves a valid synthesis selection unchanged', () => {
    const loaded = response([backend('claude-cli', true)]);
    const current = draft({ enabled: true, backend: 'claude-cli', model: 'claude-cli-model' });

    expect(settingsForSave(current, loaded)).toBe(current);
  });
});
