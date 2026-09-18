import { formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';

export type MachineTone = 'checking' | 'failed' | 'disabled' | 'stale' | 'limited' | 'ok';

/** Legend copy for the sidebar dots, ordered healthiest first. */
export const MACHINE_TONE_LEGEND: { tone: MachineTone; label: string }[] = [
  { tone: 'ok', label: 'Connected — history is complete.' },
  { tone: 'checking', label: 'Checking the connection.' },
  { tone: 'limited', label: 'Partial history — these sessions are left out of the totals.' },
  { tone: 'stale', label: 'Offline — showing the last recorded context. Retry from Settings.' },
  { tone: 'failed', label: 'Setup or connection failed — open Settings to fix it.' },
  { tone: 'disabled', label: 'Remote collection is turned off.' },
];

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
  if (connectorFailed(machine)) return 'failed';
  if (machine.state === 'disabled') return 'disabled';
  if (machine.state === 'stale') return 'stale';
  if (machine.state === 'limited') return 'limited';
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
  if (connectorFailed(machine)) {
    return `Setup failed: ${connectorFailureCopy(machine)}. Open Settings to retry.`;
  }
  if (machine.state === 'stale') {
    return `Offline. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (machine.state === 'limited') return 'Showing the available remote history.';
  if (machine.state === 'error') return 'Connection needs attention.';
  if (machine.state === 'disabled') return 'Remote collection is disabled.';
  if (machine.sessionCount === 0) {
    return `Connected. Last checked ${lastChecked}. No recent agent sessions found.`;
  }
  return `Synced ${savedHistory} over SSH. Last checked ${lastChecked}.`;
}

/** A degraded or offline host is signalled by its dot; only these two interrupt the page. */
export function needsBanner(machine: MachineFact): boolean {
  return !isChecking(machine) && (connectorFailed(machine) || machine.state === 'error');
}
