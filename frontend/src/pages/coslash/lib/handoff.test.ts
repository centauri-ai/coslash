import { describe, expect, it } from 'vitest';
import { launchRequestPath } from '@/pages/coslash/hooks/use-launch-terminal';
import { decodeApiError } from '@/pages/coslash/lib/api';
import { handoffBrief } from '@/pages/coslash/lib/handoff';
import { remoteTestRequestInit } from '@/pages/coslash/lib/remote-api';
import { LOCAL_SOURCE_ID, type SessionDetail } from '@/pages/coslash/lib/session';
import { decodeRemoteHostSettings, decodeSettingsResponse } from '@/pages/coslash/lib/settings';

function remoteDetail(): SessionDetail {
  return {
    sourceId: 'r_0123456789abcdef',
    sourceLabel: 'gpu-server',
    eligibleForAggregates: true,
    displayStale: false,
    agent: 'codex',
    id: 'sess-1',
    name: 'Remote work',
    summary: 'Did the thing',
    status: 'idle',
    cwd: '',
    branch: null,
    repo: null,
    repoLocalOnly: false,
    files: 0,
    durationMs: 60_000,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    mtime: 1,
    entrypoint: null,
    synthesis: null,
    synthesisPending: false,
    declaredGoal: null,
    model: null,
    contextTokens: null,
    contextWindow: null,
    turns: 2,
    toolUses: 0,
    errors: 0,
    compactions: 0,
    firstPrompt: 'Start',
    commands: [],
    commits: [],
    prs: 0,
    todos: [],
    digest: [],
    fileEdits: [],
    git: null,
    lastEditAt: null,
  };
}

describe('handoffBrief', () => {
  it('formats missing remote environment facts without literal undefined', () => {
    const brief = handoffBrief(remoteDetail());
    expect(brief).toContain('- Repository: —');
    expect(brief).toContain('- Branch: —');
    expect(brief).toContain('- Working directory: —');
    expect(brief).not.toContain('undefined');
  });
});

describe('launchRequestPath', () => {
  it('includes the full source-aware key', () => {
    expect(launchRequestPath({ sourceId: LOCAL_SOURCE_ID, agent: 'codex', id: 'abc' }, 'resume')).toBe(
      '/api/launch?source=local&agent=codex&id=abc&mode=resume',
    );
    expect(launchRequestPath({ sourceId: 'r_0123456789abcdef', agent: 'claude', id: 'xyz' }, 'new')).toBe(
      '/api/launch?source=r_0123456789abcdef&agent=claude&id=xyz&mode=new',
    );
  });
});

describe('settings and remote API decoders', () => {
  it('decodes optional remote settings and guide path', () => {
    expect(decodeRemoteHostSettings(null)).toBeNull();
    expect(
      decodeRemoteHostSettings({ id: 'r_0123456789abcdef', sshAlias: 'gpu-server', enabled: true }),
    ).toEqual({ id: 'r_0123456789abcdef', sshAlias: 'gpu-server', enabled: true });

    const decoded = decodeSettingsResponse({
      settings: {
        $schema: 'x',
        version: 1,
        synthesis: { enabled: false, backend: '', model: '' },
        appearance: { theme: 'light' },
        launch: { terminal: 'Terminal' },
        remote: { id: 'r_0123456789abcdef', sshAlias: 'gpu-server', enabled: true },
      },
      persisted: true,
      valid: true,
      options: {
        synthesisBackends: [],
        terminals: [],
        remoteInstallationGuidePath: 'docs/remote-host-installation.md',
      },
    });
    expect(decoded.settings.remote?.sshAlias).toBe('gpu-server');
    expect(decoded.options.remoteInstallationGuidePath).toBe('docs/remote-host-installation.md');
  });

  it('decodes stable API errors and builds remote test requests', () => {
    expect(decodeApiError({ code: 'remote_disabled', error: 'remote disabled' })).toEqual({
      code: 'remote_disabled',
      error: 'remote disabled',
    });
    expect(remoteTestRequestInit('gpu-server')).toEqual({
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sshAlias: 'gpu-server' }),
    });
  });
});
