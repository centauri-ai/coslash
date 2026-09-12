import { formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';

export function connectorFailed(machine: MachineFact) {
  return (
    machine.sourceId !== LOCAL_SOURCE_ID &&
    machine.helper?.compatible === false &&
    machine.helper.reason != null
  );
}

export function connectorFailureCopy(machine: MachineFact) {
  return machine.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

export function isLoadingEarlierHistory(machine: MachineFact) {
  return machine.refreshing && machine.reason === 'broader_history' && machine.coverageSinceMs != null;
}

export function machineActivityCopy(machine: MachineFact, checking: boolean) {
  if (machine.sourceId === LOCAL_SOURCE_ID) return 'Local Mac is up to date.';
  const lastChecked = machine.lastCheckedAtMs == null ? 'not yet' : formatTimeAgo(machine.lastCheckedAtMs);
  const savedHistory =
    machine.lastSuccessAtMs == null ? 'no saved history' : formatTimeAgo(machine.lastSuccessAtMs);
  if (isLoadingEarlierHistory(machine)) {
    return `Loading earlier history. Saved coverage begins ${new Date(machine.coverageSinceMs!).toLocaleString()}. Displayed sessions are available now.`;
  }
  if (checking) {
    return `Checking SSH. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (connectorFailed(machine))
    return `Setup failed: ${connectorFailureCopy(machine)}. Open Settings to retry.`;
  if (machine.state === 'stale') {
    return `Offline. Last checked ${lastChecked}. Saved history from ${savedHistory}.`;
  }
  if (machine.state === 'limited') return 'Showing the available remote history.';
  if (machine.state === 'error') return 'Connection needs attention.';
  if (machine.state === 'disabled') return 'Remote collection is disabled.';
  if (machine.sessionCount === 0)
    return `Connected. Last checked ${lastChecked}. No recent agent sessions found.`;
  return `Synced ${savedHistory} over SSH. Last checked ${lastChecked}.`;
}
