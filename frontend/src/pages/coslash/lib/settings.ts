import { isSynthesisEligible, type Session } from '@/pages/coslash/lib/session';

export type SynthesisSettings = {
  enabled: boolean;
  backend: string;
  model: string;
};

export type RemoteHostSettings = {
  id?: string;
  sshAlias: string;
  enabled: boolean;
  executables?: RemoteExecutableSettings;
};

export type RemoteExecutableAgent = 'claude' | 'codex' | 'opencode';

export type RemoteExecutableSettings = Partial<Record<RemoteExecutableAgent, string>>;

// Request-only intent sent beside a proposed settings replacement. It is not
// serialized into settings.json.
export type RemoteOwnershipAction = 'release' | 'uninstall';

export type CoslashSettings = {
  $schema: string;
  version: number;
  synthesis: SynthesisSettings;
  appearance: { theme: 'light' | 'dark' };
  launch: { terminal: string };
  remote?: RemoteHostSettings | null;
};

export type ModelOption = {
  id: string;
  label: string;
  default: boolean;
};

export type BackendOption = {
  id: string;
  label: string;
  models: ModelOption[];
  available: boolean;
};

export type TerminalOption = {
  id: string;
  label: string;
  available: boolean;
};

export type SettingsResponse = {
  settings: CoslashSettings;
  persisted: boolean;
  valid: boolean;
  error?: string;
  options: {
    synthesisBackends: BackendOption[];
    terminals: TerminalOption[];
  };
};

export function decodeRemoteHostSettings(value: unknown): RemoteHostSettings | null {
  if (value == null) return null;
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid remote settings');
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.sshAlias !== 'string' || typeof raw.enabled !== 'boolean') {
    throw new Error('Invalid remote settings');
  }
  const remote: RemoteHostSettings = {
    sshAlias: raw.sshAlias,
    enabled: raw.enabled,
  };
  if (raw.id != null) {
    if (typeof raw.id !== 'string') throw new Error('Invalid remote settings');
    remote.id = raw.id;
  }
  if (raw.executables != null) {
    remote.executables = decodeRemoteExecutableSettings(raw.executables);
  }
  return remote;
}

export function decodeRemoteExecutableSettings(value: unknown): RemoteExecutableSettings {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid remote executable settings');
  }
  const raw = value as Record<string, unknown>;
  if (Object.keys(raw).length === 0) {
    throw new Error('Invalid remote executable settings');
  }
  const executables: RemoteExecutableSettings = {};
  for (const agent of ['claude', 'codex', 'opencode'] as const) {
    const path = raw[agent];
    if (path == null) continue;
    if (typeof path !== 'string' || path === '' || remoteExecutablePathError(path) != null) {
      throw new Error(`Invalid remote executable path for ${agent}`);
    }
    executables[agent] = path;
  }
  if (Object.keys(raw).some((key) => !['claude', 'codex', 'opencode'].includes(key))) {
    throw new Error('Invalid remote executable settings');
  }
  return executables;
}

// A blank draft value means "automatic discovery" and is omitted on save.
// Nonblank values must follow the same structural contract as the backend.
export function remoteExecutablePathError(path: string): string | null {
  if (path === '') return null;
  if (path.length > 4096 || /[\0\r\n]/.test(path)) {
    return 'Use an absolute path or a path beginning with ~/.';
  }
  return (path.startsWith('/') && path !== '/') || (path.startsWith('~/') && path.length > 2)
    ? null
    : 'Use an absolute path or a path beginning with ~/.';
}

export function decodeSettingsResponse(value: unknown): SettingsResponse {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid settings response');
  }
  const raw = value as Record<string, unknown>;
  if (raw.settings == null || typeof raw.settings !== 'object' || Array.isArray(raw.settings)) {
    throw new Error('Invalid settings response');
  }
  if (raw.options == null || typeof raw.options !== 'object' || Array.isArray(raw.options)) {
    throw new Error('Invalid settings response');
  }
  const settingsRaw = raw.settings as Record<string, unknown>;
  const response = value as SettingsResponse;
  return {
    ...response,
    settings: {
      ...response.settings,
      remote: decodeRemoteHostSettings(settingsRaw.remote),
    },
    options: response.options,
  };
}

export function settingsForSave(settings: CoslashSettings): CoslashSettings {
  if (settings.remote == null) return settings;
  const entries = Object.entries(settings.remote.executables ?? {}).filter(
    (entry): entry is [string, string] => typeof entry[1] === 'string' && entry[1] !== '',
  );
  const executables = Object.fromEntries(entries) as RemoteExecutableSettings;
  const includeExecutables = entries.length > 0;
  const hasNormalizedExecutables =
    (settings.remote.executables != null) !== includeExecutables ||
    Object.keys(settings.remote.executables ?? {}).length !== entries.length;
  if (settings.remote.id != null && !hasNormalizedExecutables) return settings;

  const { executables: _discarded, ...remote } = settings.remote;
  const id =
    remote.id ??
    `r_${Array.from(crypto.getRandomValues(new Uint8Array(8)), (value) => value.toString(16).padStart(2, '0')).join('')}`;
  return {
    ...settings,
    remote: {
      ...remote,
      id,
      ...(includeExecutables ? { executables } : {}),
    },
  };
}

export function availableSynthesisBackends(options: readonly BackendOption[]): BackendOption[] {
  return options.filter((option) => option.available);
}

export function modelForBackend(response: SettingsResponse, backend: string): string {
  const option = response.options.synthesisBackends.find((candidate) => candidate.id === backend);
  return option?.models.find((model) => model.default)?.id ?? option?.models[0]?.id ?? '';
}

export function initialSettingsDraft(response: SettingsResponse): CoslashSettings {
  const synthesis = { ...response.settings.synthesis };

  if (!response.persisted) {
    const availableBackend = availableSynthesisBackends(response.options.synthesisBackends)[0];
    synthesis.enabled = false;
    if (availableBackend) {
      synthesis.backend = availableBackend.id;
      synthesis.model = modelForBackend(response, availableBackend.id);
    }
  }

  return {
    ...response.settings,
    synthesis,
    appearance: { ...response.settings.appearance },
    launch: { ...response.settings.launch },
    remote: response.settings.remote
      ? {
          ...response.settings.remote,
          ...(response.settings.remote.executables
            ? { executables: { ...response.settings.remote.executables } }
            : {}),
        }
      : response.settings.remote,
  };
}

export function requiresFirstRunConsent(response: SettingsResponse | null): boolean {
  return response != null && !response.persisted;
}

export function shouldPromptForSynthesisConsent(
  session: Pick<Session, 'turns' | 'compactions' | 'contextTokens'> | null,
  response: SettingsResponse | null,
): boolean {
  return session != null && requiresFirstRunConsent(response) && isSynthesisEligible(session);
}
