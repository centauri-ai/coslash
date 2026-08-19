import { isSynthesisEligible, type Session } from '@/pages/coslash/lib/session';

export type SynthesisSettings = {
  enabled: boolean;
  backend: string;
  model: string;
};

export type CoslashSettings = {
  $schema: string;
  version: number;
  synthesis: SynthesisSettings;
  appearance: { theme: 'light' | 'dark' };
  launch: { terminal: string };
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

export function availableSynthesisBackends(options: readonly BackendOption[]): BackendOption[] {
  return options.filter((option) => option.available);
}

export function modelForBackend(response: SettingsResponse, backend: string): string {
  const option = response.options.synthesisBackends.find((candidate) => candidate.id === backend);
  return option?.models.find((model) => model.default)?.id ?? option?.models[0]?.id ?? '';
}

/** Turn synthesis on only when a detected CLI exists; otherwise return null. */
export function enableSynthesisDraft(
  draft: CoslashSettings,
  response: SettingsResponse,
): CoslashSettings | null {
  const availableBackend = availableSynthesisBackends(response.options.synthesisBackends)[0];
  if (availableBackend == null) return null;
  return {
    ...draft,
    synthesis: {
      ...draft.synthesis,
      enabled: true,
      backend: availableBackend.id,
      model: modelForBackend(response, availableBackend.id),
    },
  };
}

/**
 * Settings that are safe to persist: synthesis stays on only with a detected
 * backend. Used so first-run consent can always Save instead of getting stuck.
 */
export function settingsForSave(draft: CoslashSettings, response: SettingsResponse): CoslashSettings {
  if (!draft.synthesis.enabled) return draft;
  const backends = availableSynthesisBackends(response.options.synthesisBackends);
  const selected = backends.find((option) => option.id === draft.synthesis.backend) ?? backends[0];
  if (selected == null) {
    return { ...draft, synthesis: { ...draft.synthesis, enabled: false } };
  }
  if (selected.id === draft.synthesis.backend) return draft;
  return {
    ...draft,
    synthesis: {
      ...draft.synthesis,
      backend: selected.id,
      model: modelForBackend(response, selected.id),
    },
  };
}

export function initialSettingsDraft(response: SettingsResponse): CoslashSettings {
  const synthesis = { ...response.settings.synthesis };

  if (!response.persisted) {
    const availableBackend = availableSynthesisBackends(response.options.synthesisBackends)[0];
    synthesis.enabled = availableBackend != null;
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
