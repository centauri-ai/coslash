export type DiagnosticStatus = 'ok' | 'warn' | 'fail';
export type DiagnosticSourceState = 'ok' | 'empty' | 'missing' | 'unreadable';

export type DiagnosticsCheck = {
  id: string;
  title: string;
  status: DiagnosticStatus;
  detail: string;
  fix: string;
};

export type DiagnosticSource = {
  agent: string;
  label: string;
  root: string;
  state: DiagnosticSourceState;
  entries: number;
  sessions: number;
  skipped: { path: string; error: string }[];
  skippedTotal: number;
  error: string;
  cli: { name: string; found: boolean; path: string; version: string };
};

export type RemoteDiagnostics = {
  sourceId?: string;
  label?: string;
  state: string;
  complete: boolean;
  reason?: string;
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
  nextRetryAtMs?: number;
  error?: string;
  diagnosticStderr?: string;
};

export type Diagnostics = {
  version: string;
  generatedAt: number;
  platform: { os: string; arch: string; terminalLaunchSupported: boolean };
  storage: { home: string; writable: boolean; error: string };
  synthesis: { enabled: boolean; model: string; cliFound: boolean; reason: string; error: string };
  sources: DiagnosticSource[];
  remote?: RemoteDiagnostics;
  checks: DiagnosticsCheck[];
};

const STATUS_WEIGHT: Record<DiagnosticStatus, number> = { ok: 0, warn: 1, fail: 2 };

export function worstStatus(checks: DiagnosticsCheck[]): DiagnosticStatus {
  return checks.reduce<DiagnosticStatus>(
    (worst, check) => (STATUS_WEIGHT[check.status] > STATUS_WEIGHT[worst] ? check.status : worst),
    'ok',
  );
}

export function formatDiagnosticsForCopy(snapshot: Diagnostics): string {
  const lines = [
    `coSlash ${snapshot.version} diagnostics`,
    `Generated: ${new Date(snapshot.generatedAt).toISOString()}`,
    `Platform: ${snapshot.platform.os}/${snapshot.platform.arch}; terminal launch=${snapshot.platform.terminalLaunchSupported}`,
    `Storage: ${snapshot.storage.home}; writable=${snapshot.storage.writable}`,
    `Synthesis: enabled=${snapshot.synthesis.enabled}; model=${snapshot.synthesis.model || 'unknown'}; CLI found=${snapshot.synthesis.cliFound}`,
    '',
    'Sources:',
  ];
  for (const source of snapshot.sources) {
    lines.push(
      `- ${source.label}: ${source.state}; root=${source.root}; entries=${source.entries}; sessions=${source.sessions}; skipped=${source.skippedTotal}`,
      `  CLI: found=${source.cli.found}; path=${source.cli.path || 'unknown'}; version=${source.cli.version || 'unknown'}`,
    );
    for (const skipped of source.skipped) lines.push(`  Skipped: ${skipped.error}`);
  }
  if (snapshot.remote) {
    lines.push('', 'Remote host:');
    lines.push(
      `- alias=${snapshot.remote.label ?? 'unknown'}; state=${snapshot.remote.state}; complete=${snapshot.remote.complete}`,
    );
    if (snapshot.remote.collectorVersion || snapshot.remote.schemaVersion) {
      lines.push(
        `  collector=${snapshot.remote.collectorVersion || 'unknown'}; schema=${snapshot.remote.schemaVersion || 'unknown'}`,
      );
    }
    if (snapshot.remote.nextRetryAtMs != null) {
      lines.push(`  nextRetryAtMs=${snapshot.remote.nextRetryAtMs}`);
    }
    if (snapshot.remote.error) lines.push(`  error=${snapshot.remote.error}`);
  }
  lines.push('', 'Checks:');
  for (const check of snapshot.checks) {
    lines.push(`- [${check.status}] ${check.title}: ${check.detail}`);
    if (check.fix) lines.push(`  Fix: ${check.fix}`);
  }
  return lines.join('\n');
}
