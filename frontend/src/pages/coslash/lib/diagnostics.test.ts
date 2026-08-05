import { describe, expect, it } from 'vitest';
import {
  formatDiagnosticsForCopy,
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
    storage: { home: '~/.coslash', writable: true, error: '' },
    synthesis: { enabled: true, model: 'test-model', cliFound: true, reason: '' },
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
