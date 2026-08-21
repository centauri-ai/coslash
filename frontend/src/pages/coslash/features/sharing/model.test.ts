import { describe, expect, it } from 'vitest';
import type { SnapshotPreview } from '@/pages/coslash/lib/preview';
import type { Session } from '@/pages/coslash/lib/session';
import {
  bindPreviewConsent,
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
};

function session(id: string, repo: string, mtime: number): Session {
  return {
    sourceId: 'local',
    sourceLabel: 'This Mac',
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

function preview(sourceRevision: number, hash = `sha256:${'a'.repeat(64)}`): SnapshotPreview {
  const snapshot = { schemaVersion: 'session-snapshot/v1', contentHash: hash } as SnapshotPreview['snapshot'];
  const payload = JSON.stringify(snapshot);
  return {
    adapterVersion: 'snapshot-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    sourceRevision,
    schemaVersion: 'session-snapshot/v1',
    mediaType: 'application/vnd.coslash.session-snapshot.v1+json',
    payloadBytes: new TextEncoder().encode(payload).byteLength,
    maxPayloadBytes: 262_144,
    canonicalPayloadBase64: btoa(payload),
    snapshot,
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

  it('binds approval to exact canonical bytes, source revision, and destination', () => {
    const chosen = candidates[0]!.session;
    const exact = preview(chosen.mtime);
    const item = bindPreviewConsent(chosen, exact, destination, 'synthetic-idempotency-key-0001');
    expect(item.consent.contentHash).toBe('a'.repeat(64));
    expect(consentStillCurrent(item, chosen, exact, destination)).toBe(true);
    expect(
      consentStillCurrent(item, chosen, preview(chosen.mtime, `sha256:${'b'.repeat(64)}`), destination),
    ).toBe(false);
    expect(consentStillCurrent(item, chosen, exact, { ...destination, workspaceId: 'changed' })).toBe(false);
    expect(consentStillCurrent(item, { ...chosen, mtime: chosen.mtime + 1 }, exact, destination)).toBe(false);
    expect(() =>
      bindPreviewConsent(chosen, preview(chosen.mtime + 1), destination, 'synthetic-idempotency-key-0001'),
    ).toThrow(/source revision/);
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
          sessionId: 'session',
          revisionId: 'revision',
          deduplicated: false,
          sharedAt: '2026-08-18T18:00:00Z',
          briefState: 'pending',
          route: {
            hubContractVersion: 'hub-read/v1',
            repositoryId: 'repo',
            canonicalWeekStart: '2026-08-17',
            path: '/repos/repo/sessions/2026-08-17',
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
          error: { code: 'consent_stale', retryable: true },
        },
      ],
    };
    const plan = planShareRetry(result);
    expect([...plan.unchanged]).toEqual(['codex:old']);
    expect([...plan.renewedReview]).toEqual(['codex:stale']);
    expect(primarySuccessRoute(result)?.path).toBe('/repos/repo/sessions/2026-08-17');
    expect(result.results[0]!.state !== 'failed' && result.results[0]!.briefState).toBe('pending');
  });

  it('publishes a complete retry decision for every stable error', () => {
    expect(Object.keys(RETRY_RULES)).toHaveLength(16);
    expect(RETRY_RULES.timeout).toEqual(expect.objectContaining({ renewedReview: false }));
    expect(RETRY_RULES.destination_changed).toEqual(expect.objectContaining({ renewedReview: true }));
    expect(RETRY_RULES.source_deleted).toEqual(expect.objectContaining({ renewedReview: false }));
  });

  it('refreshes authority before recovering destination and credential failures', () => {
    const result: ShareResult = {
      contractVersion: 'hub-share/v1',
      requestId: 'request',
      state: 'failed',
      results: (['destination_changed', 'credential_revoked', 'consent_stale'] as const).map(
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
