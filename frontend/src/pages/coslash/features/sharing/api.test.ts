import { afterEach, describe, expect, it, vi } from 'vitest';
import { beginHubPairing, loadHubDestination, pollHubPairing, prepareBackup, submitHubShare } from './api';

function installBrowser(fetchMock: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('window', {
    location: { hash: '', pathname: '/', search: '' },
    history: { state: null, replaceState: vi.fn() },
    sessionStorage: { getItem: vi.fn(() => null), setItem: vi.fn() },
  });
  vi.stubGlobal('fetch', fetchMock);
}

describe('Hub sharing local adapter', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('loads the server-derived destination without exposing credentials', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        contractVersion: 'hub-share/v1',
        state: 'ready',
        configured: true,
        hubUrl: 'https://hub.example.test',
        destination: {
          workspaceId: 'workspace-1',
          workspaceName: 'Compiler Team',
          currentMemberCount: 2,
          resultingMemberCount: 2,
          currentApprovedSessionCount: 3,
          historyDisclosure: 'Current members can see approved revisions.',
          credentialState: 'paired',
          audienceVersion: 'audience-v1',
        },
      }),
    );
    installBrowser(fetchMock);
    const result = await loadHubDestination();
    expect(result.state).toBe('ready');
    if (result.state !== 'ready') throw new Error('expected ready destination');
    expect(result.destination.workspaceName).toBe('Compiler Team');
    expect(JSON.stringify(result)).not.toContain('credential-secret');
  });

  it('prepares complete backups through the local authenticated adapter', async () => {
    const hash = 'a'.repeat(64);
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        adapterVersion: 'backup-preview/v1',
        state: 'ready',
        approvalAllowed: true,
        selection: { sourceKind: 'local', sourceId: 'local', agent: 'codex', sessionId: 'one' },
        bundleId: hash,
        sourceRevision: 'source-revision',
        coverage: { artifactCount: 1, artifactCounts: [], totalBytes: 1, revisionSha256: hash, problems: [] },
        capability: {
          serverId: 'server',
          maxBackupBytes: 100,
          maxBackupChunkBytes: 10,
          backupWorkspaceBytes: 1000,
          backupUploadExpiresSeconds: 3600,
        },
        audienceVersion: 'audience-v1',
      }),
    );
    installBrowser(fetchMock);
    await expect(
      prepareBackup({ sourceKind: 'local', sourceId: 'local', agent: 'codex', sessionId: 'one' }),
    ).resolves.toEqual(expect.objectContaining({ bundleId: hash }));
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/hub/backup-previews',
      expect.objectContaining({ method: 'POST' }),
    );
  });

  it('preserves collector problems on non-ready previews with an empty inventory', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        adapterVersion: 'backup-preview/v1',
        state: 'blocked',
        approvalAllowed: false,
        selection: { sourceKind: 'local', sourceId: 'local', agent: 'claude', sessionId: 'one' },
        coverage: {
          artifactCount: 0,
          artifactCounts: null,
          totalBytes: 0,
          revisionSha256: '',
          problems: [],
        },
        problem: {
          code: 'complete_backup_unsupported',
          message: 'Complete backups are not supported for this agent.',
          action: 'Choose a Codex session.',
          retryable: false,
        },
      }),
    );
    installBrowser(fetchMock);

    await expect(
      prepareBackup({ sourceKind: 'local', sourceId: 'local', agent: 'claude', sessionId: 'one' }),
    ).resolves.toEqual(
      expect.objectContaining({
        state: 'blocked',
        problem: expect.objectContaining({ code: 'complete_backup_unsupported' }),
      }),
    );
  });

  it('passes cancellation through preparation and upload requests', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          adapterVersion: 'backup-preview/v1',
          state: 'blocked',
          approvalAllowed: false,
          selection: { sourceKind: 'local', sourceId: 'local', agent: 'codex', sessionId: 'one' },
          coverage: {},
          problem: {
            code: 'temporary_unavailable',
            message: 'Unavailable.',
            action: 'Retry.',
            retryable: true,
          },
        }),
      )
      .mockResolvedValueOnce(
        Response.json({
          contractVersion: 'hub-share/v1',
          requestId: 'request-1',
          state: 'succeeded',
          results: [],
        }),
      );
    installBrowser(fetchMock);
    const controller = new AbortController();

    await prepareBackup(
      { sourceKind: 'local', sourceId: 'local', agent: 'codex', sessionId: 'one' },
      controller.signal,
    );
    await submitHubShare(
      { contractVersion: 'hub-share/v1', requestId: 'request-1', items: [] },
      controller.signal,
    );

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/hub/backup-previews',
      expect.objectContaining({ signal: controller.signal }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/hub/shares',
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('uses only local authenticated endpoints for pairing and approved shares', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ state: 'pending', pairingId: 'pair-1' }, { status: 201 }))
      .mockResolvedValueOnce(Response.json({ state: 'paired' }))
      .mockResolvedValueOnce(
        Response.json({
          contractVersion: 'hub-share/v1',
          requestId: 'request-1',
          state: 'succeeded',
          results: [],
        }),
      );
    installBrowser(fetchMock);

    await beginHubPairing();
    await pollHubPairing('pair-1');
    await submitHubShare({ contractVersion: 'hub-share/v1', requestId: 'request-1', items: [] });

    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/hub/pairings',
      '/api/hub/pairings/pair-1/poll',
      '/api/hub/shares',
    ]);
    expect(fetchMock.mock.calls[2]?.[1]).toEqual(
      expect.objectContaining({ method: 'POST', body: expect.stringContaining('"requestId":"request-1"') }),
    );
  });

  it('rejects malformed local adapter responses before they reach the share UI', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        contractVersion: 'hub-share/v1',
        state: 'ready',
        configured: true,
        destination: { workspaceName: 'missing authority and audience fields' },
      }),
    );
    installBrowser(fetchMock);
    await expect(loadHubDestination()).rejects.toThrow('outside the expected contract');
  });

  it.each(['future_error', 'toString', '__proto__'])('rejects unknown share error code %s', async (code) => {
    const fetchMock = vi.fn().mockResolvedValue(
      Response.json({
        contractVersion: 'hub-share/v1',
        requestId: 'request-1',
        state: 'failed',
        results: [
          {
            localSessionId: 'codex:one',
            idempotencyKey: 'key-000000000000',
            state: 'failed',
            deduplicated: false,
            error: { code, retryable: true },
          },
        ],
      }),
    );
    installBrowser(fetchMock);
    await expect(
      submitHubShare({ contractVersion: 'hub-share/v1', requestId: 'request-1', items: [] }),
    ).rejects.toThrow('outside the expected contract');
  });
});
