import type { MachineFact, MachineReason, MachineState } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';

export type HostStripAction = 'retry' | 'diagnostics' | 'installation';

export type HostStripModel = {
  role: 'status' | 'alert';
  message: string;
  actions: HostStripAction[];
  retryDisabled: boolean;
  tone: 'warning' | 'danger';
};

const REMOTE_LAUNCH = 'remote-launch/v1';

export function remoteMachine(machines: readonly MachineFact[]): MachineFact | null {
  return machines.find((machine) => machine.sourceId !== LOCAL_SOURCE_ID) ?? null;
}

export function remoteConfigured(machines: readonly MachineFact[]): boolean {
  return remoteMachine(machines) != null;
}

export function hostStripVisible(machine: MachineFact | null): boolean {
  if (machine == null) return false;
  return machine.state !== 'ok' && machine.state !== 'disabled';
}

function snapshotAgeLabel(lastSuccessAtMs: number | undefined, nowMs: number): string {
  if (lastSuccessAtMs == null) return 'a previous snapshot';
  const mins = Math.max(1, Math.floor((nowMs - lastSuccessAtMs) / MINUTE));
  return `a snapshot from ${mins}m ago`;
}

function stripMessage(machine: MachineFact, nowMs: number): string {
  const label = machine.label;
  switch (machine.state) {
    case 'connecting':
      if (machine.reason === 'broader_history') {
        return `${label} · refreshing remote history · showing the previous snapshot`;
      }
      return `${label} · connecting…`;
    case 'limited':
      if (machine.reason === 'refresh_timeout') {
        return `${label} · remote history took too long to refresh`;
      }
      return `${label} · remote history is too large · showing the newest sessions`;
    case 'setup_required':
      return `${label} · collector not found at ~/.local/bin/coslash`;
    case 'upgrade_required':
      return `${label} · remote collector needs an update`;
    case 'stale':
      return `${label} · unreachable · showing ${snapshotAgeLabel(machine.lastSuccessAtMs, nowMs)}`;
    case 'error':
      return `${label} · could not load remote sessions`;
    default:
      return `${label} · remote host needs attention`;
  }
}

function stripActions(state: MachineState, reason: MachineReason | undefined): HostStripAction[] {
  switch (state) {
    case 'connecting':
      return reason === 'broader_history' ? ['retry'] : [];
    case 'limited':
    case 'stale':
    case 'error':
      return ['retry', 'diagnostics'];
    case 'setup_required':
    case 'upgrade_required':
      return ['installation', 'retry'];
    default:
      return ['retry'];
  }
}

export function hostStripModel(
  machine: MachineFact,
  options: { nowMs?: number; retryInFlight?: boolean } = {},
): HostStripModel {
  const nowMs = options.nowMs ?? Date.now();
  const retryInFlight = options.retryInFlight === true || machine.state === 'connecting';
  return {
    role: machine.state === 'connecting' ? 'status' : 'alert',
    message: stripMessage(machine, nowMs),
    actions: stripActions(machine.state, machine.reason),
    retryDisabled: retryInFlight,
    tone: machine.state === 'error' || machine.state === 'stale' ? 'danger' : 'warning',
  };
}

export type RemoteLaunchGate = { allowed: true } | { allowed: false; reason: string };

export function remoteLaunchGate(machine: MachineFact | null, agent: string): RemoteLaunchGate {
  if (machine == null || machine.state === 'disabled') {
    return { allowed: false, reason: 'remote disabled' };
  }
  if (!(machine.capabilities ?? []).includes(REMOTE_LAUNCH)) {
    return { allowed: false, reason: 'remote upgrade required' };
  }
  if (!(machine.launchableAgents ?? []).includes(agent)) {
    return { allowed: false, reason: 'remote agent unavailable' };
  }
  return { allowed: true };
}

export function formatTestConnectionResult(machine: MachineFact): string {
  const platform =
    machine.hostOs != null && machine.hostArch != null ? `${machine.hostOs}/${machine.hostArch}` : null;
  const collector = machine.collectorVersion ? `collector ${machine.collectorVersion}` : null;
  if (machine.state === 'ok') {
    const launchable = (machine.launchableAgents ?? []).length > 0;
    const parts = ['Connected', collector, platform].filter(Boolean);
    return `${parts.join(' · ')}${launchable ? ' · Resume and handoff ready' : ''}`;
  }
  if (machine.state === 'setup_required') {
    return `${machine.label} · collector not found at ~/.local/bin/coslash`;
  }
  if (machine.state === 'upgrade_required') {
    return `${machine.label} · remote collector needs an update`;
  }
  if (machine.error) return `${machine.label} · ${machine.error}`;
  return `${machine.label} · ${machine.state.replaceAll('_', ' ')}`;
}

export function firstTimeSshHint(alias: string): string {
  return `If this is the first connection, run ssh ${alias} once in a terminal to accept the host key and authenticate.`;
}
