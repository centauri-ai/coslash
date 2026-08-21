import type { MachineFact, MachineReason, MachineState } from '@/pages/coslash/lib/machines';
import { LOCAL_SOURCE_ID } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';

export type HostStripAction = 'retry' | 'diagnostics';

export type HostStripModel = {
  role: 'status' | 'alert';
  message: string;
  actions: HostStripAction[];
  retryDisabled: boolean;
  tone: 'warning' | 'danger';
};

export function remoteMachine(machines: readonly MachineFact[]): MachineFact | null {
  return machines.find((machine) => machine.sourceId !== LOCAL_SOURCE_ID) ?? null;
}

export function remoteConfigured(machines: readonly MachineFact[]): boolean {
  return remoteMachine(machines) != null;
}

export function hostStripVisible(machine: MachineFact | null): boolean {
  return machine != null && machine.state !== 'ok' && machine.state !== 'disabled';
}

function snapshotAgeLabel(lastSuccessAtMs: number | undefined, nowMs: number): string {
  if (lastSuccessAtMs == null) return 'the last saved view';
  const minutes = Math.max(1, Math.floor((nowMs - lastSuccessAtMs) / MINUTE));
  return `the view from ${minutes}m ago`;
}

function reasonMessage(reason: MachineReason | undefined): string {
  switch (reason) {
    case 'sftp_unavailable':
      return 'the SFTP subsystem is unavailable';
    case 'permission_denied':
      return 'agent data is not readable';
    case 'no_supported_data':
      return 'no Claude or Codex data was found';
    case 'partial_agent_data':
      return 'some agent data is unavailable';
    case 'history_truncated':
      return 'showing the newest sessions within safety limits';
    case 'refresh_timeout':
      return 'the refresh timed out';
    case 'authentication_failed':
      return 'SSH authentication failed';
    case 'host_key_failed':
      return 'SSH host key verification failed';
    case 'invalid_remote_data':
      return 'agent data could not be parsed';
    case 'local_cache_failed':
      return 'the Mac cache could not be updated';
    default:
      return 'the SFTP refresh failed';
  }
}

function stripMessage(machine: MachineFact, nowMs: number): string {
  switch (machine.state) {
    case 'connecting':
      return machine.reason === 'broader_history'
        ? `${machine.label} · refreshing older history · showing the previous view`
        : `${machine.label} · connecting over SFTP…`;
    case 'limited':
      return `${machine.label} · ${reasonMessage(machine.reason)}`;
    case 'stale':
      return `${machine.label} · unreachable · showing ${snapshotAgeLabel(machine.lastSuccessAtMs, nowMs)}`;
    case 'error':
      return `${machine.label} · ${reasonMessage(machine.reason)}`;
    default:
      return `${machine.label} · remote host needs attention`;
  }
}

function stripActions(state: MachineState, reason: MachineReason | undefined): HostStripAction[] {
  if (state === 'connecting' && reason !== 'broader_history') return [];
  return ['retry', 'diagnostics'];
}

export function hostStripModel(
  machine: MachineFact,
  options: { nowMs?: number; retryInFlight?: boolean } = {},
): HostStripModel {
  return {
    role: machine.state === 'connecting' ? 'status' : 'alert',
    message: stripMessage(machine, options.nowMs ?? Date.now()),
    actions: stripActions(machine.state, machine.reason),
    retryDisabled: options.retryInFlight === true || machine.state === 'connecting',
    tone: machine.state === 'error' || machine.state === 'stale' ? 'danger' : 'warning',
  };
}

export function formatTestConnectionResult(machine: MachineFact): string {
  if (machine.state === 'ok') return 'Connected · SFTP is ready · no Linux coSlash installation needed';
  return `${machine.label} · ${machine.error || reasonMessage(machine.reason)}`;
}

export function firstTimeSshHint(alias: string): string {
  return `Run ssh ${alias} once in Terminal if OpenSSH still needs host-key confirmation or authentication.`;
}
