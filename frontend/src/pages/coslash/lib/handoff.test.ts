import { describe, expect, it } from 'vitest';
import { launchRequestPath } from '@/pages/coslash/hooks/use-launch-terminal';
import { decodeApiError } from '@/pages/coslash/lib/api';
import { handoffBrief } from '@/pages/coslash/lib/handoff';
import {
  decodeHelperSetup,
  HELPER_SETUP_OUTCOMES,
  helperSetupRequestInit,
  remoteTestRequestInit,
} from '@/pages/coslash/lib/remote-api';
import { decodeMachineFact, HELPER_STATES, MACHINE_REASONS } from '@/pages/coslash/lib/machines';
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
  it('includes the full local source-aware key and rejects remote launch', () => {
    expect(launchRequestPath({ sourceId: LOCAL_SOURCE_ID, agent: 'codex', id: 'abc' }, 'resume')).toBe(
      '/api/launch?source=local&agent=codex&id=abc&mode=resume',
    );
    expect(() =>
      launchRequestPath({ sourceId: 'r_0123456789abcdef', agent: 'claude', id: 'xyz' }, 'new'),
    ).toThrow('remote launch unsupported');
  });
});

describe('settings and remote API decoders', () => {
  it('decodes optional remote settings', () => {
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
      },
    });
    expect(decoded.settings.remote?.sshAlias).toBe('gpu-server');
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

  it('decodes helper transport, lifecycle, and safe metrics', () => {
    expect(
      decodeMachineFact({
        sourceId: 'r_0123456789abcdef',
        label: 'gpu-server',
        state: 'limited',
        complete: false,
        reason: 'output_limit',
        transport: 'helper',
        helper: { state: 'deprecated', version: 'v1', compatible: true, fallback: false, reason: 'helper_upgrade_required' },
        metrics: { requestBytes: 120, responseBytes: 456, records: 3, roundTripMs: 40 },
      }),
    ).toMatchObject({
      transport: 'helper',
      helper: { state: 'deprecated', version: 'v1', compatible: true },
      metrics: { responseBytes: 456, records: 3 },
    });
  });

  it('exhaustively decodes helper lifecycle states and reasons', () => {
    for (const state of HELPER_STATES) {
      for (const reason of MACHINE_REASONS) {
        expect(
          decodeMachineFact({
            sourceId: 'r_0123456789abcdef', label: 'gpu-server', state: 'limited', complete: false,
            transport: 'sftp', helper: { state, compatible: false, fallback: true, reason },
          }).helper,
        ).toMatchObject({ state, reason });
      }
    }
  });

  it('sends one explicit install or upgrade consent', () => {
    expect(helperSetupRequestInit('install').body).toBe(JSON.stringify({ install: true, upgrade: false }));
    expect(helperSetupRequestInit('upgrade').body).toBe(JSON.stringify({ install: false, upgrade: true }));
  });

  it('exhaustively decodes setup outcomes and recorded helper ownership', () => {
    for (const outcome of HELPER_SETUP_OUTCOMES) {
      expect(
        decodeHelperSetup({
          outcome,
          machine: {
            sourceId: 'r_0123456789abcdef', label: 'gpu-server', state: 'limited', complete: false,
            helperOwnershipRecorded: true,
            helper: { state: 'sftp', compatible: false, fallback: true, reason: 'helper_missing' },
          },
        }),
      ).toMatchObject({ outcome, machine: { helperOwnershipRecorded: true } });
    }
    expect(() =>
      decodeHelperSetup({
        outcome: 'green',
        machine: { sourceId: 'r_0123456789abcdef', label: 'gpu-server', state: 'limited', complete: false },
      }),
    ).toThrow('Expected one of');
  });
});
