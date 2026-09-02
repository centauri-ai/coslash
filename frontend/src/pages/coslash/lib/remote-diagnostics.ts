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
  if (remote.transport) head.push(remote.transport);
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

  if (remote.helperState) {
    const helper = [`helper ${remote.helperState}`];
    if (remote.helperVersion) helper.push(remote.helperVersion);
    helper.push(remote.helperCompatible ? 'compatible' : 'not executable');
    if (remote.helperFallback) helper.push('SFTP fallback');
    lines.push(helper.join(' · '));
  }
  const metrics: string[] = [];
  if (remote.requestBytes != null) metrics.push(`${remote.requestBytes} request bytes`);
  if (remote.responseBytes != null) metrics.push(`${remote.responseBytes} response bytes`);
  if (remote.records != null) metrics.push(`${remote.records} records`);
  if (metrics.length > 0) lines.push(metrics.join(' · '));

  if (remote.error) lines.push(`error: ${remote.error}`);
  return lines;
}
