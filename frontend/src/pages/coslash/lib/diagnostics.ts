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
  transcripts: number;
  sessions: number;
  skipped: { path: string; error: string }[];
  skippedTotal: number;
  error: string;
  cli: { name: string; found: boolean; path: string; version: string };
};

export type Diagnostics = {
  version: string;
  generatedAt: number;
  platform: { os: string; arch: string; terminalLaunchSupported: boolean };
  storage: { home: string; writable: boolean; summaries: number; error: string };
  synthesis: { enabled: boolean; model: string; cliFound: boolean; reason: string };
  settings: null;
  sources: DiagnosticSource[];
  checks: DiagnosticsCheck[];
};

const STATUS_WEIGHT: Record<DiagnosticStatus, number> = { ok: 0, warn: 1, fail: 2 };

export function worstStatus(checks: DiagnosticsCheck[]): DiagnosticStatus {
  return checks.reduce<DiagnosticStatus>(
    (worst, check) => (STATUS_WEIGHT[check.status] > STATUS_WEIGHT[worst] ? check.status : worst),
    'ok',
  );
}

export function uncoveredSources(snapshot: Diagnostics): DiagnosticSource[] {
  return snapshot.sources.filter((source) => source.state !== 'ok' || source.skippedTotal > 0);
}

export function sourceCoverageMessage(sources: DiagnosticSource[]): string {
  return sources
    .map((source) => {
      if (source.state === 'missing') return `${source.label} not detected`;
      if (source.state === 'empty') return `No ${source.label} sessions found`;
      if (source.state === 'unreadable') return `${source.label} session scan failed`;
      return `${source.label} scan skipped ${source.skippedTotal} unreadable ${source.skippedTotal === 1 ? 'path' : 'paths'}`;
    })
    .join('; ');
}

export function formatDiagnosticsForCopy(snapshot: Diagnostics): string {
  const lines = [
    `coSlash ${snapshot.version} diagnostics`,
    `Generated: ${new Date(snapshot.generatedAt).toISOString()}`,
    `Platform: ${snapshot.platform.os}/${snapshot.platform.arch}; terminal launch=${snapshot.platform.terminalLaunchSupported}`,
    `Storage: ${snapshot.storage.home}; writable=${snapshot.storage.writable}; summaries=${snapshot.storage.summaries}`,
    `Synthesis: enabled=${snapshot.synthesis.enabled}; model=${snapshot.synthesis.model || 'unknown'}; CLI found=${snapshot.synthesis.cliFound}`,
    '',
    'Sources:',
  ];
  for (const source of snapshot.sources) {
    lines.push(
      `- ${source.label}: ${source.state}; root=${source.root}; transcripts=${source.transcripts}; sessions=${source.sessions}; skipped=${source.skippedTotal}`,
      `  CLI: found=${source.cli.found}; path=${source.cli.path || 'unknown'}; version=${source.cli.version || 'unknown'}`,
    );
    for (const skipped of source.skipped) lines.push(`  Skipped: ${skipped.path}: ${skipped.error}`);
  }
  lines.push('', 'Checks:');
  for (const check of snapshot.checks) {
    lines.push(`- [${check.status}] ${check.title}: ${check.detail}`);
    if (check.fix) lines.push(`  Fix: ${check.fix}`);
  }
  return lines.join('\n');
}
