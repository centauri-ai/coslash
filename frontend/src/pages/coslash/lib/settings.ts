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

export function modelForBackend(response: SettingsResponse, backend: string): string {
  const option = response.options.synthesisBackends.find((candidate) => candidate.id === backend);
  return option?.models.find((model) => model.default)?.id ?? option?.models[0]?.id ?? '';
}

export function initialSettingsDraft(response: SettingsResponse): CoslashSettings {
  const synthesis = { ...response.settings.synthesis };

  if (!response.persisted) {
    const availableBackend = response.options.synthesisBackends.find((option) => option.available);
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
