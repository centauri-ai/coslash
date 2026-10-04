import { describe, expect, it } from 'vitest';
import type { Session } from '@/pages/coslash/lib/session';
import {
  backupSelection,
  bindBackupConsent,
  consentStillCurrent,
  filterShareCandidates,
  hubRouteURL,
  isCanonicalBackupRoute,
  limitShareSelection,
  localSessionId,
  localShareCandidates,
  mergeShareItemResults,
  planShareRetry,
  primarySuccessRoute,
  reconcileVisibleSelection,
  RETRY_RULES,
  shareBatchSummary,
  shareResultState,
  toggleCandidateGroup,
  type BackupPreview,
  type ShareCandidate,
  type ShareDestination,
  type ShareItemResult,
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

describe('hub-share/v2 complete-backup consumer', () => {
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

  it('applies the share cap in caller selection order', () => {
    const values = Array.from({ length: 101 }, (_, index) => ({
      session: session(String(index), 'coslash', now),
      previouslyShared: false,
    }));
    const lastKey = localSessionId(values[100]!.session);
    const selected = toggleCandidateGroup(new Set([lastKey]), values.slice(0, 100));
    const limited = limitShareSelection(selected, values);
    expect(limited.size).toBe(100);
    expect(limited.has(lastKey)).toBe(true);
    expect(limited.has(localSessionId(values[99]!.session))).toBe(false);
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
      contractVersion: 'hub-share/v2',
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

  it('separates same-key network retry from renewed review and permanent validation failure', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v2',
      requestId: 'request',
      state: 'failed',
      results: [
        {
          localSessionId: 'network-item',
          idempotencyKey: 'network-key-0001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'network_unavailable', retryable: true },
        },
        {
          localSessionId: 'review-item',
          idempotencyKey: 'review-key-0001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'stale_backup_review', retryable: true },
        },
        {
          localSessionId: 'invalid-item',
          idempotencyKey: 'invalid-key-0001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'backup_manifest_invalid', retryable: false },
        },
      ],
    };
    const plan = planShareRetry(result);
    expect([...plan.unchanged]).toEqual(['network-item']);
    expect([...plan.renewedReview]).toEqual(['review-item']);
    expect(plan.unchanged.has('invalid-item')).toBe(false);
    expect(plan.renewedReview.has('invalid-item')).toBe(false);
  });

  it('publishes a complete retry decision for every stable error', () => {
    expect(Object.keys(RETRY_RULES)).toHaveLength(23);
    expect(RETRY_RULES.timeout).toEqual(expect.objectContaining({ renewedReview: false }));
    expect(RETRY_RULES.destination_changed).toEqual(expect.objectContaining({ renewedReview: true }));
    expect(RETRY_RULES.source_deleted).toEqual(expect.objectContaining({ renewedReview: false }));
    expect(RETRY_RULES.temporary_unavailable).toEqual(
      expect.objectContaining({
        renewedReview: false,
        reason: expect.stringContaining('confirm the upload result'),
        action: expect.stringContaining('original keys'),
      }),
    );
    expect(RETRY_RULES.stale_backup_review).toEqual(
      expect.objectContaining({ renewedReview: true, action: expect.stringContaining('Review') }),
    );
  });

  it('summarizes identical all-fail items without exposing their internal IDs', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v2',
      requestId: 'request',
      state: 'failed',
      results: ['private-id-one', 'private-id-two'].map((localSessionId) => ({
        localSessionId,
        idempotencyKey: `key-${localSessionId}-0001`,
        state: 'failed' as const,
        deduplicated: false as const,
        error: { code: 'temporary_unavailable' as const, retryable: true },
      })),
    };
    const summary = shareBatchSummary(result, 'Compiler Team');
    expect(summary.title).toBe('Share failed');
    expect(summary.detail).toContain('None of the 2 selected sessions were shared');
    expect(summary.detail).toContain(RETRY_RULES.temporary_unavailable.reason);
    expect(summary.detail).not.toContain('private-id');
    expect(shareResultState(result.results)).toBe('failed');
  });

  it('keeps shared, private, and failed outcomes distinct in mixed batches', () => {
    const privateItem = {
      localSessionId: 'private-id',
      idempotencyKey: 'private-key-0001',
      state: 'private',
      private: true,
      deduplicated: true,
      sharingNotice: 'This backup is private in My space.',
    } as const;
    const result: ShareResult = {
      contractVersion: 'hub-share/v2',
      requestId: 'request',
      state: 'partial',
      results: [
        {
          localSessionId: 'shared-id',
          idempotencyKey: 'shared-key-0001',
          state: 'accepted',
          revisionId: 'revision',
          deduplicated: false,
          sharedAt: '2026-09-22T20:00:00Z',
          route: {
            hubContractVersion: 'session-backup-read/v1',
            repositoryId: 'repository',
            path: '/v3/session-backups/revision',
          },
        },
        privateItem,
        {
          localSessionId: 'failed-id',
          idempotencyKey: 'failed-key-0001',
          state: 'failed',
          deduplicated: false,
          error: { code: 'network_unavailable', retryable: true },
        },
      ],
    };
    const summary = shareBatchSummary(result, 'Compiler Team');
    expect(summary.detail).toContain('1 shared with Compiler Team');
    expect(summary.detail).toContain('1 saved privately in My space');
    expect(summary.detail).toContain('1 failed item');
    expect(shareResultState(result.results)).toBe('partial');
  });

  it('replaces retried failures while keeping prior accepted and private items stable', () => {
    const first: ShareItemResult = {
      localSessionId: 'accepted-id',
      idempotencyKey: 'accepted-key-0001',
      state: 'accepted',
      revisionId: 'revision-one',
      deduplicated: false,
      sharedAt: '2026-09-22T20:00:00Z',
      route: {
        hubContractVersion: 'session-backup-read/v1',
        repositoryId: 'repository-one',
        path: '/v3/session-backups/revision-one',
      },
    };
    const privateItem: ShareItemResult = {
      localSessionId: 'private-id',
      idempotencyKey: 'private-key-0001',
      state: 'private',
      private: true,
      deduplicated: true,
      sharingNotice: 'This backup is private in My space.',
    };
    const priorFailure: ShareItemResult = {
      localSessionId: 'retry-id',
      idempotencyKey: 'retry-key-0001',
      state: 'failed',
      deduplicated: false,
      error: { code: 'temporary_unavailable', retryable: true },
    };
    const retried: ShareItemResult = {
      localSessionId: 'retry-id',
      idempotencyKey: priorFailure.idempotencyKey,
      state: 'accepted',
      revisionId: 'revision-two',
      deduplicated: true,
      sharedAt: '2026-09-22T20:01:00Z',
      route: {
        hubContractVersion: 'session-backup-read/v1',
        repositoryId: 'repository-two',
        path: '/v3/session-backups/revision-two',
      },
    };
    const merged = mergeShareItemResults([first, privateItem, priorFailure], [retried]);
    expect(merged.map((item) => item.localSessionId)).toEqual(['accepted-id', 'private-id', 'retry-id']);
    expect(merged[0]).toEqual(first);
    expect(merged[1]).toEqual(privateItem);
    expect(merged[2]).toEqual(retried);
    expect(shareResultState(merged)).toBe('partial');
  });

  it('refreshes authority before recovering destination and credential failures', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v2',
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

  it('preserves a path-prefixed Hub URL for canonical backup handoffs', () => {
    expect(hubRouteURL('https://hub.example.test/coSlash', '/v3/session-backups/revision-one')).toBe(
      'https://hub.example.test/coSlash/v3/session-backups/revision-one',
    );
    expect(isCanonicalBackupRoute('revision-one', '/v3/session-backups/revision-one')).toBe(true);
    expect(isCanonicalBackupRoute('revision-one', 'https://evil.example/revision-one')).toBe(false);
    expect(isCanonicalBackupRoute('revision-one', '/v3/session-backups/revision-two')).toBe(false);
    expect(isCanonicalBackupRoute('revision-one', '/v2/session-revisions/revision-one')).toBe(false);
    expect(() => hubRouteURL('https://hub.example.test', 'https://evil.example/revision-one')).toThrow(
      'outside the expected contract',
    );
    expect(
      hubRouteURL(
        'https://hub.example.test/coSlash',
        '/v2/sources/source/agents/codex/sessions/session/revisions/revision-one',
      ),
    ).toBe(
      'https://hub.example.test/coSlash/v2/sources/source/agents/codex/sessions/session/revisions/revision-one',
    );
    expect(() => hubRouteURL('https://hub.example.test/coSlash', '/../outside')).toThrow(
      'outside the expected contract',
    );
  });
});
