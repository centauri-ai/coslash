import { LoaderCircleIcon } from 'lucide-react';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { formatTimeAgo } from '@/pages/coslash/lib/format';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import { getStatus, LOCAL_SOURCE_ID, type Session } from '@/pages/coslash/lib/session';

function activityFor(sessions: readonly Session[], sourceId: string) {
  const sourceSessions = sessions.filter((session) => session.sourceId === sourceId);
  return {
    active: sourceSessions.filter((session) => getStatus(session.status) === 'busy').length,
    waiting: sourceSessions.filter((session) => getStatus(session.status) === 'waiting').length,
  };
}

function isChecking(machine: MachineFact) {
  return machine.refreshing || machine.reason === 'initial_refresh' || machine.state === 'connecting';
}

function machineCopy(machine: MachineFact, checking: boolean) {
  if (machine.sourceId === LOCAL_SOURCE_ID) return 'This Mac is up to date.';
  const lastChecked = machine.lastCheckedAtMs == null ? 'not yet' : formatTimeAgo(machine.lastCheckedAtMs);
  const savedHistory =
    machine.lastSuccessAtMs == null ? 'no saved history' : formatTimeAgo(machine.lastSuccessAtMs);
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

function needsAttention(machine: MachineFact) {
  return machine.reason === 'authentication_failed' || machine.reason === 'host_key_failed';
}

function connectorFailed(machine: MachineFact) {
  return (
    machine.sourceId !== LOCAL_SOURCE_ID &&
    machine.helper?.compatible === false &&
    machine.helper.reason != null
  );
}

function connectorFailureCopy(machine: MachineFact) {
  return machine.helper?.reason?.replaceAll('_', ' ') ?? 'connector setup failed';
}

function tone(machine: MachineFact) {
  if (isChecking(machine)) return 'bg-info animate-pulse';
  if (connectorFailed(machine)) return 'bg-destructive';
  if (machine.state === 'disabled') return 'bg-muted-foreground';
  if (machine.state === 'stale') return 'bg-warning/60';
  if (machine.state === 'limited') return 'bg-warning';
  if (machine.state === 'error') return needsAttention(machine) ? 'bg-destructive' : 'bg-warning/60';
  return 'bg-success';
}

export function MachineActivity({
  machines,
  sessions,
  onRemoteRetry,
  remoteRetryInFlight,
}: {
  machines: MachineFact[];
  sessions: Session[];
  onRemoteRetry: () => void;
  remoteRetryInFlight: boolean;
}) {
  return (
    <TooltipProvider>
      <div className="flex shrink-0 items-center gap-1.5 text-xs">
        {machines.map((machine) => {
          const activity = activityFor(sessions, machine.sourceId);
          const refreshing =
            isChecking(machine) || (machine.sourceId !== LOCAL_SOURCE_ID && remoteRetryInFlight);
          const retryable =
            machine.sourceId !== LOCAL_SOURCE_ID &&
            (machine.state === 'stale' || machine.state === 'error' || connectorFailed(machine));
          const offline =
            machine.sourceId !== LOCAL_SOURCE_ID &&
            !refreshing &&
            (machine.state === 'stale' || machine.state === 'error');
          return (
            <Tooltip key={machine.sourceId}>
              <TooltipTrigger asChild>
                <span>
                  <button
                    type="button"
                    onClick={retryable ? onRemoteRetry : undefined}
                    disabled={!retryable || remoteRetryInFlight}
                    className="bg-muted/60 hover:bg-muted text-muted-foreground disabled:hover:bg-muted/60 inline-flex items-center gap-2 rounded-md px-2 py-1 text-left transition-colors disabled:cursor-help"
                    aria-label={retryable ? `Retry ${machine.label}` : undefined}
                  >
                    {refreshing ? (
                      <LoaderCircleIcon className="text-info size-3 animate-spin" aria-label="Refreshing" />
                    ) : (
                      <span className={`size-1.5 rounded-full ${tone(machine)}`} />
                    )}
                    <span className="text-foreground font-medium">{machine.label}</span>
                    {machine.sourceId !== LOCAL_SOURCE_ID && (
                      <span className="text-muted-foreground">SSH</span>
                    )}
                    {machine.sourceId !== LOCAL_SOURCE_ID && refreshing ? (
                      <span className="text-muted-foreground">Checking</span>
                    ) : connectorFailed(machine) ? (
                      <span className="text-destructive">Setup failed</span>
                    ) : offline ? (
                      <span className="text-muted-foreground">Offline</span>
                    ) : (
                      <span className="text-muted-foreground tabular-nums">
                        {activity.active} active · {activity.waiting} waiting
                      </span>
                    )}
                  </button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{machineCopy(machine, refreshing)}</TooltipContent>
            </Tooltip>
          );
        })}
      </div>
    </TooltipProvider>
  );
}
