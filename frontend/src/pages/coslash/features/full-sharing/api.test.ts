import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchFullSessionPreview, submitFullSessionShare } from './api';
import type { FullSessionShareRequest } from './model';

function installBrowser(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('window', {
    location: { hash: '', pathname: '/', search: '' },
    history: { state: null, replaceState: vi.fn() },
    sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
  });
  vi.stubGlobal('fetch', fetchMock);
}

const selection = {
  sourceId: 'r_0123456789abcdef',
  agent: 'codex',
  sessionId: 'session',
  revisionId: 'a'.repeat(64),
};

describe('full-session local API adapter', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('loads an exact structured preview through the authenticated loopback API', async () => {
    const hash = `sha256:${'b'.repeat(64)}`;
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        adapterVersion: 'full-session-preview/v1',
        state: 'ready',
        approvalAllowed: true,
        selection,
        schemaVersion: 'session-revision/v2',
        mediaType: 'application/vnd.coslash.session-revision.v2+json',
        recordBytes: 2297,
        payloadBytes: 2599,
        maxRecordBytes: 1040384,
        recordSha256: hash,
        embeddedSecretRisk: true,
        envelope: {
          schemaVersion: 'session-revision/v2',
          mediaType: 'application/vnd.coslash.session-revision.v2+json',
          recordByteCount: 2297,
          recordSha256: hash,
          repository: { canonical: 'github.com/centauri-ai/coslash', localOnly: false },
          record: { sourceId: selection.sourceId, revisionId: selection.revisionId },
        },
      }),
    );
    installBrowser(fetchMock);
    const preview = await fetchFullSessionPreview(selection);
    expect(preview.envelope?.record).toEqual(expect.objectContaining({ revisionId: selection.revisionId }));
    expect(fetchMock.mock.calls[0]?.[0]).toContain('/api/hub/full-session-preview?');
  });

  it('rejects an error response that leaks a full content envelope', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        adapterVersion: 'full-session-preview/v1',
        state: 'oversized',
        approvalAllowed: false,
        embeddedSecretRisk: false,
        selection,
        problem: { code: 'full_session_too_large', message: 'Too large', action: 'Choose another.' },
        envelope: { record: { secret: 'must not be returned' } },
      }),
    );
    installBrowser(fetchMock);
    await expect(fetchFullSessionPreview(selection)).rejects.toThrow('outside the expected contract');
  });

  it('accepts a content-free retryable discovery failure', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        adapterVersion: 'full-session-preview/v1',
        state: 'unavailable',
        approvalAllowed: false,
        embeddedSecretRisk: false,
        selection,
        problem: {
          code: 'network_unavailable',
          message: 'The Hub could not be reached for its capability check.',
          action: 'Retry without changing the selected revision.',
        },
      }),
    );
    installBrowser(fetchMock);
    const preview = await fetchFullSessionPreview(selection);
    expect(preview.state).toBe('unavailable');
    expect(preview.envelope).toBeUndefined();
  });

  it('posts the consent binding and accepts the canonical v2 route only', async () => {
    const request = {
      contractVersion: 'full-session-share/v1',
      selection,
      idempotencyKey: 'full-session-key-0001',
      consent: {
        previewContractVersion: 'full-session-preview/v1',
        revisionId: selection.revisionId,
        recordSha256: `sha256:${'b'.repeat(64)}`,
        recordBytes: 2297,
        payloadBytes: 2599,
        repository: { canonical: 'github.com/centauri-ai/coslash', localOnly: false },
        destinationWorkspaceId: 'workspace',
        destinationName: 'Compiler Team',
        audienceMemberCount: 2,
      },
    } satisfies FullSessionShareRequest;
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        contractVersion: 'full-session-share/v1',
        state: 'accepted',
        selection,
        idempotencyKey: request.idempotencyKey,
        repositoryId: 'repo',
        byteCount: 2297,
        contentSha256: request.consent.recordSha256,
        deduplicated: false,
        sharedAt: '2026-09-17T12:00:00Z',
        route: { hubContractVersion: 'full-session-read/v1', path: `/v2/revisions/${selection.revisionId}` },
      }),
    );
    installBrowser(fetchMock);
    const result = await submitFullSessionShare(request);
    expect(result.state).toBe('accepted');
    expect(fetchMock.mock.calls[0]).toEqual([
      '/api/hub/full-session-shares',
      expect.objectContaining({ method: 'POST', body: expect.stringContaining(selection.revisionId) }),
    ]);
  });
});
