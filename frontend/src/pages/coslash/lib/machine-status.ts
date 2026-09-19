import { formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact, MachineReason } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';

export type MachineTone = 'checking' | 'failed' | 'disabled' | 'stale' | 'incomplete' | 'truncated' | 'ok';

/** The dot colour per tone. Its tokens only resolve inside `.coslash-shell`. */
export const MACHINE_TONE_DOT: Record<MachineTone, string> = {
  checking: 'bg-coslash-accent animate-pulse',
  failed: 'bg-coslash-clay-dot',
  disabled: 'bg-coslash-neutral-dot',
  stale: 'bg-coslash-amber-dot',
  incomplete: 'bg-coslash-amber-dot',
  truncated: 'bg-coslash-green-dot',
  ok: 'bg-coslash-green-dot',
};

// The host answered and the helper ran; only the data came back short. Any
// other reason, including an absent one, stays offline so an unreachable host
// is never reported as connected.
const REACHABLE_REASONS: readonly MachineReason[] = [
  'partial_agent_data',
  'history_truncated',
  'broader_history',
  'invalid_remote_data',
  'local_cache_failed',
  'no_supported_data',
  'permission_denied',
];

/** Only a host whose own refresh fell short recovers from another one. */
export function machineRetryable(machine: MachineFact): boolean {
  if (machine.sourceId === LOCAL_SOURCE_ID) return false;
  const tone = machineTone(machine);
  return tone === 'stale' || tone === 'incomplete';
}

/** Only a failed connector needs the consented setup flow; a failed credential retries. */
export function needsSetup(machine: MachineFact): boolean {
  return connectorFailed(machine);
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
  return machine.reason === 'authentication_failed' || machine.reason === 'host_key_failed';
}

export function machineTone(machine: MachineFact): MachineTone {
  if (isChecking(machine)) return 'checking';
  if (machine.state === 'disabled') return 'disabled';
  if (connectorFailed(machine)) return 'failed';
  if (machine.state === 'stale') {
    return machine.reason != null && REACHABLE_REASONS.includes(machine.reason) ? 'incomplete' : 'stale';
  }
  if (machine.state === 'limited') return 'truncated';
  if (machine.state === 'error') return needsAttention(machine) ? 'failed' : 'stale';
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
  if (machineTone(machine) === 'incomplete') {
    return `Connected. Synced ${savedHistory}; the refresh ${lastChecked} did not complete.`;
  }
  if (machine.state === 'stale') {
    return `Offline. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (machine.state === 'limited') {
    return 'Connected. Older history was truncated, so these sessions are left out of the totals.';
  }
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
