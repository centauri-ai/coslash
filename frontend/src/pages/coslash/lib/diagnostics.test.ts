import { describe, expect, it } from 'vitest';
import {
  formatDiagnosticsForCopy,
  sourceCoverageMessage,
  uncoveredSources,
  worstStatus,
  type Diagnostics,
  type DiagnosticsCheck,
} from '@/pages/coslash/lib/diagnostics';

const check = (status: DiagnosticsCheck['status']): DiagnosticsCheck => ({
  id: status,
  title: status,
  status,
  detail: status,
  fix: '',
});

function snapshot(): Diagnostics {
  return {
    version: 'dev',
    generatedAt: 0,
    platform: { os: 'darwin', arch: 'arm64', terminalLaunchSupported: true },
    storage: { home: '~/.coslash', writable: true, summaries: 2, error: '' },
    synthesis: { enabled: true, model: 'test-model', cliFound: true, reason: '' },
    settings: null,
    sources: [
      {
        agent: 'claude',
        label: 'Claude Code',
        root: '~/.claude/projects',
        state: 'ok',
        transcripts: 4,
        sessions: 3,
        skipped: [],
        skippedTotal: 0,
        error: '',
        cli: { name: 'claude', found: true, path: '/opt/bin/claude', version: '1.0' },
      },
      {
        agent: 'codex',
        label: 'Codex',
        root: '~/.codex/sessions',
        state: 'missing',
        transcripts: 0,
        sessions: 0,
        skipped: [],
        skippedTotal: 0,
        error: '',
        cli: { name: 'codex', found: false, path: '', version: '' },
      },
    ],
    checks: [check('ok')],
  };
}

describe('diagnostics helpers', () => {
  it('returns the worst check status', () => {
    expect(worstStatus([])).toBe('ok');
    expect(worstStatus([check('ok'), check('warn')])).toBe('warn');
    expect(worstStatus([check('warn'), check('fail'), check('ok')])).toBe('fail');
  });

  it('identifies missing and empty agents', () => {
    const value = snapshot();
    expect(uncoveredSources(value).map((source) => source.agent)).toEqual(['codex']);
    value.sources[0].state = 'empty';
    expect(uncoveredSources(value).map((source) => source.agent)).toEqual(['claude', 'codex']);
    expect(sourceCoverageMessage(value.sources)).toBe('No Claude Code sessions found; Codex not detected');
  });

  it('identifies failed and partial source scans', () => {
    const value = snapshot();
    value.sources[0].state = 'unreadable';
    value.sources[1].state = 'ok';
    value.sources[1].skippedTotal = 2;
    expect(uncoveredSources(value).map((source) => source.agent)).toEqual(['claude', 'codex']);
    expect(sourceCoverageMessage(value.sources)).toBe(
      'Claude Code session scan failed; Codex scan skipped 2 unreadable paths',
    );
  });

  it('formats safe diagnostic facts without session content', () => {
    const value = snapshot() as Diagnostics & {
      privatePath: string;
      sessions: { name: string; transcript: string }[];
    };
    value.privatePath = '/Users/alice/.claude/projects/private.jsonl';
    value.sessions = [{ name: 'secret session name', transcript: 'private transcript content' }];
    const output = formatDiagnosticsForCopy(value);
    expect(output).toContain('~/.claude/projects');
    expect(output).toContain('transcripts=4; sessions=3');
    expect(output).not.toContain('/Users/alice');
    expect(output).not.toContain('secret session name');
    expect(output).not.toContain('private transcript content');
  });
});
