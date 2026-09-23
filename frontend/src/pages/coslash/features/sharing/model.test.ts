import { describe, expect, it } from 'vitest';
import type { Session } from '@/pages/coslash/lib/session';
import {
  backupSelection,
  bindBackupConsent,
  completeBackupUnsupportedReason,
  consentStillCurrent,
  filterShareCandidates,
  hubRouteURL,
  localSessionId,
  localShareCandidates,
  planShareRetry,
  primarySuccessRoute,
  reconcileVisibleSelection,
  RETRY_RULES,
  toggleCandidateGroup,
  type BackupPreview,
  type ShareCandidate,
  type ShareDestination,
  type ShareResult,
} from './model';

const destination: ShareDestination = {
  workspaceId: '10000000-0000-4000-8000-000000000001',
  workspaceName: 'Compiler Team',
  currentMemberCount: 2,
  resultingMemberCount: 2,
  currentApprovedSessionCount: 3,
  historyDisclosure: 'Current members can see approved revisions.',
  credentialState: 'paired',
  audienceVersion: 'audience-v1',
};

function session(id: string, repo: string, mtime: number): Session {
  return {
    sourceId: 'local',
    sourceLabel: 'Local Mac',
    detailRevision: String(mtime),
    eligibleForAggregates: true,
    displayStale: false,
    agent: 'codex',
    id,
    name: `Session ${id}`,
    summary: null,
    status: null,
    cwd: `/src/${repo}`,
    branch: 'main',
    repo,
    repoLocalOnly: false,
    files: 1,
    durationMs: 1,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    mtime,
    entrypoint: null,
    synthesis: null,
    synthesisPending: false,
    declaredGoal: null,
    model: null,
    contextTokens: null,
    contextWindow: null,
    turns: 1,
    toolUses: 1,
    errors: 0,
    compactions: 0,
    firstPrompt: null,
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

function preview(chosen: Session, hash = 'a'.repeat(64)): BackupPreview {
  return {
    adapterVersion: 'backup-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    selection: backupSelection(chosen),
    bundleId: hash,
    sourceRevision: 'source-revision',
    coverage: {
      artifactCount: 2,
      artifactCounts: [
        { kind: 'raw-transcript', count: 1 },
        { kind: 'parsed-session-record', count: 1 },
      ],
      totalBytes: 4096,
      revisionSha256: hash,
      problems: [],
    },
    capability: {
      serverId: 'server-v3',
      maxBackupBytes: 1 << 30,
      maxBackupChunkBytes: 1 << 20,
      backupWorkspaceBytes: 50 * (1 << 30),
      backupUploadExpiresSeconds: 86_400,
    },
    audienceVersion: destination.audienceVersion,
  };
}

describe('localShareCandidates', () => {
  it('excludes remote sessions before agent:id Hub keying', () => {
    const local = { session: session('local-1', 'coslash', 10), previouslyShared: false };
    const remote = {
      session: { ...session('remote-1', 'coslash', 10), sourceId: 'r_0123456789abcdef', sourceLabel: 'gpu' },
      previouslyShared: false,
    };
    expect(localShareCandidates([local, remote])).toEqual([local]);
  });
});

describe('complete backup support', () => {
  it('keeps Codex selectable and gives every other agent a visible blocker', () => {
    expect(completeBackupUnsupportedReason({ agent: 'codex' })).toBeNull();
    expect(completeBackupUnsupportedReason({ agent: 'claude' })).toBe(
      'Complete backup v1 supports Codex sessions only.',
    );
    expect(completeBackupUnsupportedReason({ agent: 'cursor' })).toBe(
      'Complete backup v1 supports Codex sessions only.',
    );
    expect(completeBackupUnsupportedReason({ agent: 'opencode' })).toBe(
      'Complete backup v1 supports Codex sessions only.',
    );
  });
});

describe('hub-share/v1 public consumer', () => {
  const now = Date.UTC(2026, 7, 18);
  const candidates: ShareCandidate[] = [
    { session: session('new', 'alpha', now - 2 * 86_400_000), previouslyShared: false },
    { session: session('old', 'beta', now - 20 * 86_400_000), previouslyShared: true },
  ];

  it('filters deterministically and deselects newly hidden approvals', () => {
    const all = filterShareCandidates(candidates, '', 'all', now);
    const selected = toggleCandidateGroup(new Set(), all);
    expect(selected.size).toBe(2);
    const narrowed = filterShareCandidates(candidates, 'alpha', '7d', now);
    expect([...reconcileVisibleSelection(selected, narrowed)]).toEqual([
      localSessionId(candidates[0]!.session),
    ]);
  });

  it('keeps local and SSH candidates with matching vendor IDs independently bound', () => {
    const local = session('same-id', 'alpha', now);
    const ssh = {
      ...session('same-id', 'alpha', now),
      sourceId: 'r_0123456789abcdef',
      sourceLabel: 'SSH workspace',
    };
    expect(localSessionId(local)).toBe('local:codex:same-id');
    expect(localSessionId(ssh)).toBe('r_0123456789abcdef:codex:same-id');
  });

  it('binds approval to the complete hash, source, destination, audience, and capacity', () => {
    const chosen = candidates[0]!.session;
    const exact = preview(chosen);
    const item = bindBackupConsent(chosen, exact, destination, 'synthetic-idempotency-key-0001');
    expect(item.consent.completeBackupSha256).toBe('a'.repeat(64));
    expect(consentStillCurrent(item, chosen, exact, destination)).toBe(true);
    expect(consentStillCurrent(item, chosen, preview(chosen, 'b'.repeat(64)), destination)).toBe(false);
    expect(consentStillCurrent(item, chosen, exact, { ...destination, workspaceId: 'changed' })).toBe(false);
    expect(consentStillCurrent(item, { ...chosen, mtime: chosen.mtime + 1 }, exact, destination)).toBe(false);
    expect(consentStillCurrent(item, chosen, exact, { ...destination, audienceVersion: 'changed' })).toBe(
      false,
    );
    expect(
      consentStillCurrent(
        item,
        chosen,
        { ...exact, capability: { ...exact.capability!, maxBackupBytes: 1 } },
        destination,
      ),
    ).toBe(false);
  });

  it('preserves only retryable partial failures and returns the canonical success route', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v1',
      requestId: 'request',
      state: 'partial',
      results: [
        {
          localSessionId: 'codex:new',
          idempotencyKey: 'key-accepted-0000001',
          state: 'accepted',
          revisionId: 'revision',
          deduplicated: false,
          sharedAt: '2026-08-18T18:00:00Z',
          route: {
            hubContractVersion: 'session-backup-read/v1',
            repositoryId: 'repo',
            path: '/v3/session-backups/revision',
          },
        },
        {
          localSessionId: 'codex:old',
          idempotencyKey: 'key-failed-00000001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'temporary_unavailable', retryable: true },
        },
        {
          localSessionId: 'codex:stale',
          idempotencyKey: 'key-stale-0000000001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'stale_backup_review', retryable: true },
        },
      ],
    };
    const plan = planShareRetry(result);
    expect([...plan.unchanged]).toEqual(['codex:old']);
    expect([...plan.renewedReview]).toEqual(['codex:stale']);
    expect(primarySuccessRoute(result)?.path).toBe('/v3/session-backups/revision');
  });

  it('publishes a complete retry decision for every stable error', () => {
    expect(Object.keys(RETRY_RULES)).toHaveLength(23);
    expect(RETRY_RULES.timeout).toEqual(expect.objectContaining({ renewedReview: false }));
    expect(RETRY_RULES.destination_changed).toEqual(expect.objectContaining({ renewedReview: true }));
    expect(RETRY_RULES.source_deleted).toEqual(expect.objectContaining({ renewedReview: false }));
  });

  it('refreshes authority before recovering destination and credential failures', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v1',
      requestId: 'request',
      state: 'failed',
      results: (['destination_changed', 'credential_revoked', 'stale_backup_review'] as const).map(
        (code, index) => ({
          localSessionId: `codex:${index}`,
          idempotencyKey: `key-${index}-000000000000`,
          state: 'failed' as const,
          deduplicated: false,
          error: { code, retryable: code !== 'credential_revoked' },
        }),
      ),
    };
    expect([...planShareRetry(result).refreshDestination]).toEqual(['codex:0']);
  });

  it('preserves a path-prefixed Hub URL for route handoffs', () => {
    expect(hubRouteURL('https://hub.example.test/coSlash', '/repos/one/sessions/week')).toBe(
      'https://hub.example.test/coSlash/repos/one/sessions/week',
    );
  });
});
