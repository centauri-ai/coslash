import type { RemoteDiagnostics } from '@/pages/coslash/lib/diagnostics';
import { MINUTE } from '@/pages/coslash/lib/time';

function formatNextRetry(nextRetryAtMs: number, nowMs: number): string {
  const mins = Math.ceil((nextRetryAtMs - nowMs) / MINUTE);
  if (mins <= 0) return 'now';
  if (mins < 60) return `in ${mins}m`;
  return `in ${Math.ceil(mins / 60)}h`;
}

export function formatRemoteDiagnosticsFacts(
  remote: RemoteDiagnostics,
  options: { sessionCount?: number; nowMs?: number } = {},
): string[] {
  const nowMs = options.nowMs ?? Date.now();
  const lines: string[] = [];
  const head = [remote.label ?? 'Remote host', remote.state];
  lines.push(head.join(' · '));

  const counts: string[] = [];
  if (options.sessionCount != null) counts.push(`${options.sessionCount} sessions`);
  if (remote.coverageSinceMs != null) {
    counts.push(`coverage since ${new Date(remote.coverageSinceMs).toISOString()}`);
  }
  if (counts.length > 0) lines.push(counts.join(' · '));

  const timing: string[] = [];
  if (remote.lastSuccessAtMs != null) {
    timing.push(`last success ${new Date(remote.lastSuccessAtMs).toISOString()}`);
  }
  if (remote.roundTripMs != null) timing.push(`round trip ${remote.roundTripMs}ms`);
  if (remote.nextRetryAtMs != null)
    timing.push(`next automatic retry ${formatNextRetry(remote.nextRetryAtMs, nowMs)}`);
  if (timing.length > 0) lines.push(timing.join(' · '));

  if (remote.error) lines.push(`error: ${remote.error}`);
  if (remote.diagnosticStderr) lines.push(`stderr: ${remote.diagnosticStderr}`);
  return lines;
}
