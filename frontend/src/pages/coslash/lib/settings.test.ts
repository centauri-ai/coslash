import { describe, expect, it } from 'vitest';
import {
  availableSynthesisBackends,
  decodeRemoteExecutableSettings,
  decodeRemoteHostSettings,
  initialSettingsDraft,
  remoteExecutablePathError,
  remoteExecutableSettingsEqual,
  settingsForSave,
  type BackendOption,
  type CoslashSettings,
  type SettingsResponse,
} from '@/pages/coslash/lib/settings';
import settingsSchemaSource from '../../../../../settings.schema.json?raw';

function backend(id: string, available: boolean): BackendOption {
  return { id, label: id, models: [], available };
}

describe('availableSynthesisBackends', () => {
  it('excludes undetected backends', () => {
    const options = [backend('claude-cli', true), backend('codex_exec', false)];

    expect(availableSynthesisBackends(options).map(({ id }) => id)).toEqual(['claude-cli']);
  });
});

describe('initialSettingsDraft', () => {
  it('keeps synthesis disabled for unpersisted settings with an available backend', () => {
    const response: SettingsResponse = {
      settings: {
        $schema: 'x',
        version: 1,
        synthesis: { enabled: false, backend: '', model: '' },
        appearance: { theme: 'light' },
        launch: { terminal: 'Terminal' },
      },
      persisted: false,
      valid: true,
      options: {
        synthesisBackends: [
          {
            id: 'claude-cli',
            label: 'Claude CLI',
            available: true,
            models: [
              { id: 'claude-sonnet', label: 'Claude Sonnet', default: false },
              { id: 'claude-opus', label: 'Claude Opus', default: true },
            ],
          },
        ],
        terminals: [],
      },
    };

    expect(initialSettingsDraft(response).synthesis).toEqual({
      enabled: false,
      backend: 'claude-cli',
      model: 'claude-opus',
    });
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

  it('omits blank executable overrides without mutating the draft', () => {
    const settings = {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: false, backend: '', model: '' },
      appearance: { theme: 'light' },
      launch: { terminal: 'Terminal' },
      remote: {
        id: 'r_0123456789abcdef',
        sshAlias: 'gpu-server',
        enabled: true,
        executables: { claude: '', codex: '~/bin/codex' },
      },
    } as CoslashSettings;

    expect(settingsForSave(settings).remote?.executables).toEqual({ codex: '~/bin/codex' });
    expect(settings.remote?.executables?.claude).toBe('');
  });
});

describe('remote executable settings', () => {
  it('decodes the supported per-agent keys', () => {
    expect(
      decodeRemoteHostSettings({
        id: 'r_0123456789abcdef',
        sshAlias: 'gpu-server',
        enabled: true,
        executables: { claude: '/opt/claude/bin/claude', codex: '~/bin/codex' },
      }),
    ).toMatchObject({ executables: { claude: '/opt/claude/bin/claude', codex: '~/bin/codex' } });
  });

  it('rejects an OpenCode override while remote OpenCode sessions are unsupported', () => {
    expect(() => decodeRemoteExecutableSettings({ opencode: '/opt/opencode/bin/opencode' })).toThrow(
      'Invalid remote executable settings',
    );
  });

  it.each(['/', 'relative/claude', '~claude', '~/', '/valid/path\n'])(
    'rejects invalid executable path %j',
    (path) => {
      expect(remoteExecutablePathError(path)).not.toBeNull();
    },
  );

  it('rejects a null override alongside a valid path', () => {
    expect(() => decodeRemoteExecutableSettings({ claude: null, codex: '/bin/codex' })).toThrow(
      'Invalid remote executable path for claude',
    );
  });

  it('rejects an explicitly null executables object', () => {
    expect(() =>
      decodeRemoteHostSettings({
        id: 'r_0123456789abcdef',
        sshAlias: 'gpu-server',
        enabled: true,
        executables: null,
      }),
    ).toThrow('Invalid remote executable settings');
  });

  it('treats blank draft values as equivalent to omitted overrides', () => {
    expect(remoteExecutableSettingsEqual({ claude: '' }, {})).toBe(true);
    expect(remoteExecutableSettingsEqual({ codex: '~/bin/codex' }, {})).toBe(false);
  });

  it.each([
    ['/bin/codex', true],
    ['~/bin/codex', true],
    ['/bin/codex\0bad', false],
    ['/bin/codex\rbad', false],
    ['/bin/codex\nbad', false],
  ] as const)('matches the published schema path contract for %j', (path, accepted) => {
    const schema = JSON.parse(settingsSchemaSource) as {
      $defs: { remoteExecutablePath: { minLength: number; maxLength: number; pattern: string } };
    };
    const definition = schema.$defs.remoteExecutablePath;
    const schemaAccepts =
      path.length >= definition.minLength &&
      path.length <= definition.maxLength &&
      new RegExp(definition.pattern).test(path);

    expect(schemaAccepts).toBe(accepted);
    expect(remoteExecutablePathError(path) == null).toBe(accepted);
  });
});
