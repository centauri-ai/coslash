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
  return {
    ...response.settings,
    synthesis: {
      ...response.settings.synthesis,
      enabled: response.persisted ? response.settings.synthesis.enabled : true,
    },
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
