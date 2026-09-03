import { assertOneOf } from '@/pages/coslash/lib/narrow';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';

export const MACHINE_STATES = ['ok', 'connecting', 'limited', 'stale', 'error', 'disabled'] as const;
export type MachineState = (typeof MACHINE_STATES)[number];
export const MACHINE_TRANSPORTS = ['sftp', 'helper'] as const;
export type MachineTransport = (typeof MACHINE_TRANSPORTS)[number];
export const HELPER_STATES = [
  'sftp',
  'ready',
  'deprecated',
  'upgrade_required',
  'unsupported',
  'revoked',
  'verification_failed',
] as const;
export type HelperState = (typeof HELPER_STATES)[number];

export const MACHINE_REASONS = [
  'initial_refresh',
  'broader_history',
  'history_truncated',
  'refresh_timeout',
  'authentication_failed',
  'host_key_failed',
  'connection_failed',
  'sftp_unavailable',
  'permission_denied',
  'no_supported_data',
  'partial_agent_data',
  'invalid_remote_data',
  'local_cache_failed',
  'helper_missing',
  'helper_not_executable',
  'helper_incompatible',
  'helper_failed',
  'output_limit',
  'helper_platform_unsupported',
  'helper_verification_failed',
  'helper_installation_failed',
  'helper_revoked',
  'helper_upgrade_required',
  'helper_rolled_back',
  'helper_consent_required',
  'disabled',
] as const;
export type MachineReason = (typeof MACHINE_REASONS)[number];

export type AgentCoverage = {
  agent: string;
  candidateFiles: number;
  selectedFiles: number;
  truncated: boolean;
  error?: string;
};

export type MachineFact = {
  sourceId: string;
  label: string;
  state: MachineState;
  complete: boolean;
  reason?: MachineReason;
  lastSuccessAtMs?: number;
  lastCheckedAtMs?: number;
  sessionCount?: number;
  coverageSinceMs?: number;
  roundTripMs?: number;
  coverage?: AgentCoverage[];
  error?: string;
  transport?: MachineTransport;
  helper?: HelperFact;
  metrics?: CollectionMetrics;
  helperInstallationAvailable?: boolean;
  helperProbeState?: 'unknown' | 'probing' | 'ready' | 'fallback';
  helperOwnershipRecorded?: boolean;
  helperOwnershipCorrupt?: boolean;
  refreshing?: boolean;
};

export type HelperFact = {
  state: HelperState;
  version?: string;
  compatible: boolean;
  fallback: boolean;
  reused?: boolean;
  reason?: MachineReason;
};

export type CollectionMetrics = {
  requestBytes?: number;
  responseBytes?: number;
  records?: number;
  roundTripMs?: number;
};

export function machinesForSourceFilter(machines: readonly MachineFact[]): MachineFact[] {
  return machines.filter(
    (machine) =>
      machine.sourceId !== LOCAL_SOURCE_ID &&
      (machine.helper?.compatible === true || machine.lastSuccessAtMs != null),
  );
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function decodeCoverage(value: unknown): AgentCoverage[] | undefined {
  if (value === undefined) return undefined;
  if (!Array.isArray(value)) throw new Error('Invalid machine coverage');
  return value.map((item) => {
    if (item == null || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error('Invalid machine coverage');
    }
    const raw = item as Record<string, unknown>;
    if (
      typeof raw.agent !== 'string' ||
      typeof raw.candidateFiles !== 'number' ||
      typeof raw.selectedFiles !== 'number' ||
      typeof raw.truncated !== 'boolean'
    ) {
      throw new Error('Invalid machine coverage');
    }
    const coverage: AgentCoverage = {
      agent: raw.agent,
      candidateFiles: raw.candidateFiles,
      selectedFiles: raw.selectedFiles,
      truncated: raw.truncated,
    };
    const error = optionalString(raw.error);
    if (error != null) coverage.error = error;
    return coverage;
  });
}

function decodeHelper(value: unknown): HelperFact | undefined {
  if (value === undefined) return undefined;
  if (value == null || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid helper state');
  const raw = value as Record<string, unknown>;
  if (
    typeof raw.state !== 'string' ||
    typeof raw.compatible !== 'boolean' ||
    typeof raw.fallback !== 'boolean'
  ) {
    throw new Error('Invalid helper state');
  }
  const helper: HelperFact = {
    state: assertOneOf(raw.state, HELPER_STATES),
    compatible: raw.compatible,
    fallback: raw.fallback,
  };
  const version = optionalString(raw.version);
  if (version != null) helper.version = version;
  if (raw.reused != null) {
    if (typeof raw.reused !== 'boolean') throw new Error('Invalid helper state');
    helper.reused = raw.reused;
  }
  if (raw.reason != null) {
    if (typeof raw.reason !== 'string') throw new Error('Invalid helper state');
    helper.reason = assertOneOf(raw.reason, MACHINE_REASONS);
  }
  return helper;
}

function decodeMetrics(value: unknown): CollectionMetrics | undefined {
  if (value === undefined) return undefined;
  if (value == null || typeof value !== 'object' || Array.isArray(value))
    throw new Error('Invalid collection metrics');
  const raw = value as Record<string, unknown>;
  const metrics: CollectionMetrics = {};
  for (const key of ['requestBytes', 'responseBytes', 'records', 'roundTripMs'] as const) {
    const number = optionalNumber(raw[key]);
    if (number != null) metrics[key] = number;
  }
  return metrics;
}

export function decodeMachineFact(value: unknown): MachineFact {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid machine fact');
  }
  const raw = value as Record<string, unknown>;
  if (
    typeof raw.sourceId !== 'string' ||
    typeof raw.label !== 'string' ||
    typeof raw.state !== 'string' ||
    typeof raw.complete !== 'boolean'
  ) {
    throw new Error('Invalid machine fact');
  }
  const fact: MachineFact = {
    sourceId: raw.sourceId,
    label: raw.label,
    state: assertOneOf(raw.state, MACHINE_STATES),
    complete: raw.complete,
  };
  if (raw.reason != null) {
    if (typeof raw.reason !== 'string') throw new Error('Invalid machine fact');
    fact.reason = assertOneOf(raw.reason, MACHINE_REASONS);
  }
  for (const key of ['lastSuccessAtMs', 'lastCheckedAtMs', 'coverageSinceMs', 'roundTripMs'] as const) {
    const number = optionalNumber(raw[key]);
    if (number != null) fact[key] = number;
  }
  if (raw.sessionCount != null) {
    if (typeof raw.sessionCount !== 'number' || !Number.isInteger(raw.sessionCount) || raw.sessionCount < 0) {
      throw new Error('Invalid machine fact');
    }
    fact.sessionCount = raw.sessionCount;
  }
  const coverage = decodeCoverage(raw.coverage);
  if (coverage != null) fact.coverage = coverage;
  const error = optionalString(raw.error);
  if (error != null) fact.error = error;
  if (raw.transport != null) {
    if (typeof raw.transport !== 'string') throw new Error('Invalid machine fact');
    fact.transport = assertOneOf(raw.transport, MACHINE_TRANSPORTS);
  }
  const helper = decodeHelper(raw.helper);
  if (helper != null) fact.helper = helper;
  const metrics = decodeMetrics(raw.metrics);
  if (metrics != null) fact.metrics = metrics;
  if (raw.helperInstallationAvailable != null) {
    if (typeof raw.helperInstallationAvailable !== 'boolean') throw new Error('Invalid machine fact');
    fact.helperInstallationAvailable = raw.helperInstallationAvailable;
  }
  if (raw.helperProbeState != null) {
    if (!['unknown', 'probing', 'ready', 'fallback'].includes(String(raw.helperProbeState))) {
      throw new Error('Invalid machine fact');
    }
    fact.helperProbeState = raw.helperProbeState as MachineFact['helperProbeState'];
  }
  if (raw.helperOwnershipRecorded != null) {
    if (typeof raw.helperOwnershipRecorded !== 'boolean') throw new Error('Invalid machine fact');
    fact.helperOwnershipRecorded = raw.helperOwnershipRecorded;
  }
  if (raw.helperOwnershipCorrupt != null) {
    if (typeof raw.helperOwnershipCorrupt !== 'boolean') throw new Error('Invalid machine fact');
    fact.helperOwnershipCorrupt = raw.helperOwnershipCorrupt;
  }
  if (raw.refreshing != null) {
    if (typeof raw.refreshing !== 'boolean') throw new Error('Invalid machine fact');
    fact.refreshing = raw.refreshing;
  }
  return fact;
}

export function decodeMachineFacts(value: unknown): MachineFact[] {
  if (!Array.isArray(value)) throw new Error('Invalid machines list');
  return value.map(decodeMachineFact);
}
