import { formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';

export type MachineTone = 'checking' | 'failed' | 'disabled' | 'stale' | 'limited' | 'ok';

/** One line per dot colour, shown when the dot is hovered. */
export const MACHINE_TONE_COPY: Record<MachineTone, string> = {
  ok: 'Connected — history is complete.',
  checking: 'Checking the connection.',
  limited: 'Partial history — these sessions are left out of the totals.',
  stale: 'Offline — showing the last recorded context.',
  failed: 'Setup or connection failed — open Settings to fix it.',
  disabled: 'Remote collection is turned off.',
};

/** Only an offline host recovers from a refresh. */
export function machineRetryable(machine: MachineFact): boolean {
  if (machine.sourceId === LOCAL_SOURCE_ID) return false;
  return machineTone(machine) === 'stale';
}

/** Only a failed connector needs the consented setup flow; a failed credential retries. */
export function needsSetup(machine: MachineFact): boolean {
  return connectorFailed(machine);
}

export function needsSettings(machine: MachineFact): boolean {
  return needsSetup(machine) || machine.actionRequired != null;
}

function isChecking(machine: MachineFact): boolean {
  return (
    machine.refreshing === true || machine.reason === 'initial_refresh' || machine.state === 'connecting'
  );
}

function connectorFailed(machine: MachineFact): boolean {
  return (
    machine.sourceId !== LOCAL_SOURCE_ID &&
    machine.helper?.compatible === false &&
    machine.helper.reason != null
  );
}

function connectorFailureCopy(machine: MachineFact): string {
  return machine.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

function needsAttention(machine: MachineFact): boolean {
  return machine.actionRequired != null || machine.reason === 'host_key_failed';
}

export function machineTone(machine: MachineFact): MachineTone {
  if (isChecking(machine)) return 'checking';
  if (machine.state === 'disabled') return 'disabled';
  if (connectorFailed(machine)) return 'failed';
  if (needsAttention(machine)) return 'failed';
  if (machine.state === 'stale') return 'stale';
  if (machine.state === 'limited') return 'limited';
  if (machine.state === 'error') return 'stale';
  return 'ok';
}

export function machineStatusText(machine: MachineFact): string {
  if (machine.sourceId === LOCAL_SOURCE_ID) return 'Local Mac is up to date.';
  const lastChecked = machine.lastCheckedAtMs == null ? 'not yet' : formatTimeAgo(machine.lastCheckedAtMs);
  const savedHistory =
    machine.lastSuccessAtMs == null ? 'no saved history' : formatTimeAgo(machine.lastSuccessAtMs);
  if (isChecking(machine)) {
    return `Checking SSH. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (machine.state === 'disabled') return 'Remote collection is disabled.';
  if (connectorFailed(machine)) {
    return `Setup failed: ${connectorFailureCopy(machine)}. Open Settings to retry.`;
  }
  if (machine.actionRequired === 'verify_host_key') {
    return 'SSH host identity changed. Verify its host key in Terminal before reconnecting.';
  }
  if (machine.actionRequired === 'authenticate') {
    return 'SSH authentication is required. Open Settings for Terminal guidance.';
  }
  if (machine.state === 'stale') {
    return `Offline. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (machine.state === 'limited') return 'Showing the available remote history.';
  if (machine.state === 'error') return 'Connection needs attention.';
  if (machine.sessionCount === 0) {
    return `Connected. Last checked ${lastChecked}. No recent agent sessions found.`;
  }
  return `Synced ${savedHistory} over SSH. Last checked ${lastChecked}.`;
}

/** A degraded or offline host is signalled by its dot; only these two interrupt the page. */
export function needsBanner(machine: MachineFact): boolean {
  if (isChecking(machine) || machine.state === 'disabled') return false;
  return connectorFailed(machine) || machine.state === 'error';
}
