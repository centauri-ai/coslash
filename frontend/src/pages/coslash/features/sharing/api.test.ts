import { afterEach, describe, expect, it, vi } from 'vitest';
import { beginHubPairing, loadHubDestination, pollHubPairing, submitHubShare } from './api';

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
