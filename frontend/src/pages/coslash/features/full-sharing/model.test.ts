import { describe, expect, it } from 'vitest';
import type { ShareDestination } from '@/pages/coslash/features/sharing/model';
import type { Session } from '@/pages/coslash/lib/session';
import {
  bindFullSessionReview,
  fullSessionCandidates,
  fullSessionReviewStillCurrent,
  type FullSessionPreview,
} from './model';

const destination: ShareDestination = {
  workspaceId: 'workspace',
  workspaceName: 'Compiler Team',
  currentMemberCount: 2,
  resultingMemberCount: 2,
  currentApprovedSessionCount: 0,
  historyDisclosure: 'Current members can see approved revisions.',
  credentialState: 'paired',
  audienceVersion: 'audience-v1',
};

function session(sourceId = 'r_0123456789abcdef'): Session {
  return {
    sourceId,
    sourceLabel: sourceId === 'local' ? 'Local Mac' : 'SSH workspace',
    detailRevision: 'a'.repeat(64),
    fullRevision: 'a'.repeat(64),
    eligibleForAggregates: true,
    displayStale: false,
    shareEligibility: 'eligible',
    agent: 'codex',
    id: 'session',
    name: 'Complete session',
    summary: null,
    status: null,
    cwd: '/workspace/coslash',
    branch: 'feature/full',
    repo: 'github.com/centauri-ai/coslash',
    repoLocalOnly: false,
    files: 1,
    durationMs: 1,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    mtime: 2,
    entrypoint: 'codex',
    synthesis: null,
    synthesisPending: false,
    declaredGoal: null,
    model: 'gpt-5',
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

function preview(chosen: Session): FullSessionPreview {
  const selection = {
    sourceId: chosen.sourceId,
    agent: chosen.agent,
    sessionId: chosen.id,
    revisionId: chosen.fullRevision!,
  };
  return {
    adapterVersion: 'full-session-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    selection,
    schemaVersion: 'session-revision/v2',
    mediaType: 'application/vnd.coslash.session-revision.v2+json',
    recordBytes: 2297,
    payloadBytes: 2599,
    maxRecordBytes: 1_040_384,
    recordSha256: `sha256:${'b'.repeat(64)}`,
    embeddedSecretRisk: true,
    envelope: {
      schemaVersion: 'session-revision/v2',
      mediaType: 'application/vnd.coslash.session-revision.v2+json',
      recordByteCount: 2297,
      recordSha256: `sha256:${'b'.repeat(64)}`,
      repository: { canonical: 'github.com/centauri-ai/coslash', localOnly: false },
      record: { sourceId: chosen.sourceId, revisionId: chosen.fullRevision },
    },
  };
}

describe('full-session v2 review binding', () => {
  it('offers only eligible exact SSH Codex records and leaves local v1 candidates alone', () => {
    const remote = session();
    expect(fullSessionCandidates([remote, session('local'), { ...remote, agent: 'claude' }])).toEqual([
      remote,
    ]);
  });

  it('binds exact content, destination name, and audience count', () => {
    const chosen = session();
    const exact = preview(chosen);
    const request = bindFullSessionReview(chosen, exact, destination, 'full-session-key-0001');
    expect(fullSessionReviewStillCurrent(request, chosen, exact, destination)).toBe(true);
    expect(
      fullSessionReviewStillCurrent(request, chosen, exact, {
        ...destination,
        currentMemberCount: 3,
      }),
    ).toBe(false);
    expect(
      fullSessionReviewStillCurrent(request, { ...chosen, fullRevision: 'c'.repeat(64) }, exact, destination),
    ).toBe(false);
    expect(request.consent).toEqual(
      expect.objectContaining({
        recordBytes: 2297,
        payloadBytes: 2599,
        destinationName: 'Compiler Team',
        audienceMemberCount: 2,
      }),
    );
  });
});
