import { assertOneOf } from '@/pages/coslash/lib/narrow';

export const MACHINE_STATES = [
  'ok',
  'connecting',
  'limited',
  'setup_required',
  'upgrade_required',
  'stale',
  'error',
  'disabled',
] as const;

export type MachineState = (typeof MACHINE_STATES)[number];

export const MACHINE_REASONS = [
  'initial_refresh',
  'broader_history',
  'history_truncated',
  'refresh_timeout',
  'collector_missing',
  'collector_outdated',
  'connection_failed',
  'collector_failed',
  'invalid_remote_transport',
  'disabled',
] as const;

export type MachineReason = (typeof MACHINE_REASONS)[number];

export type MachineFact = {
  sourceId: string;
  label: string;
  state: MachineState;
  complete: boolean;
  reason?: MachineReason;
  collectorVersion?: string;
  schemaVersion?: string;
  capabilities?: string[];
  launchableAgents?: string[];
  hostOs?: string;
  hostArch?: string;
  lastSuccessAtMs?: number;
  coverageSinceMs?: number;
  clockOffsetMs?: number;
  roundTripMs?: number;
  error?: string;
};

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' ? value : undefined;
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

function optionalStringArray(value: unknown): string[] | undefined {
  if (!Array.isArray(value) || value.some((item) => typeof item !== 'string')) return undefined;
  return value;
}

export function decodeMachineFact(value: unknown): MachineFact {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid machine fact');
  }
  const raw = value as Record<string, unknown>;
  if (typeof raw.sourceId !== 'string' || typeof raw.label !== 'string') {
    throw new Error('Invalid machine fact');
  }
  if (typeof raw.state !== 'string' || typeof raw.complete !== 'boolean') {
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
  const collectorVersion = optionalString(raw.collectorVersion);
  if (collectorVersion != null) fact.collectorVersion = collectorVersion;
  const schemaVersion = optionalString(raw.schemaVersion);
  if (schemaVersion != null) fact.schemaVersion = schemaVersion;
  const capabilities = optionalStringArray(raw.capabilities);
  if (capabilities != null) fact.capabilities = capabilities;
  const launchableAgents = optionalStringArray(raw.launchableAgents);
  if (launchableAgents != null) fact.launchableAgents = launchableAgents;
  const hostOs = optionalString(raw.hostOs);
  if (hostOs != null) fact.hostOs = hostOs;
  const hostArch = optionalString(raw.hostArch);
  if (hostArch != null) fact.hostArch = hostArch;
  const lastSuccessAtMs = optionalNumber(raw.lastSuccessAtMs);
  if (lastSuccessAtMs != null) fact.lastSuccessAtMs = lastSuccessAtMs;
  const coverageSinceMs = optionalNumber(raw.coverageSinceMs);
  if (coverageSinceMs != null) fact.coverageSinceMs = coverageSinceMs;
  const clockOffsetMs = optionalNumber(raw.clockOffsetMs);
  if (clockOffsetMs != null) fact.clockOffsetMs = clockOffsetMs;
  const roundTripMs = optionalNumber(raw.roundTripMs);
  if (roundTripMs != null) fact.roundTripMs = roundTripMs;
  const error = optionalString(raw.error);
  if (error != null) fact.error = error;
  return fact;
}

export function decodeMachineFacts(value: unknown): MachineFact[] {
  if (!Array.isArray(value)) throw new Error('Invalid machines list');
  return value.map(decodeMachineFact);
}
