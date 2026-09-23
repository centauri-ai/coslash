import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { TooltipProvider } from '@/components/ui/tooltip';
import { DiffList } from '@/pages/coslash/components/DiffList';
import {
  cachedOfflineWarning,
  detailAttemptState,
  DetailLoadError,
  detailPresentation,
  detailRequestKey,
  filePanelOpen,
  inspectorWidthForKey,
  overlayLiveSessionFields,
  refreshSourceAndRetry,
  SessionInspectorTitle,
  SessionModelUsage,
  snapshotMayBeStale,
  SnapshotRefreshStatus,
  SnapshotStalenessNotice,
  SummaryOnlyBanner,
  synthesisAttemptKey,
  synthesisMatchesSnapshot,
} from '@/pages/coslash/components/SessionInspector';
import type { FileSelection } from '@/pages/coslash/hooks/use-sessions';
import type { Session } from '@/pages/coslash/lib/session';

const session = {
  sourceId: 'r_0123456789abcdef',
  agent: 'codex',
  id: 'same-session',
  detailRevision: '',
  name: 'Bounded remote summary',
} as Session;

const selection: FileSelection = {
  sourceId: session.sourceId,
  agent: session.agent,
  sessionId: session.id,
  revision: 'revision-1',
  path: 'src/example.ts',
  changeIds: ['change-000000-000000'],
};

describe('SessionInspector exact-detail boundaries', () => {
  it('shows Cursor model switches without token usage', () => {
    const markup = renderToStaticMarkup(
      <SessionModelUsage
        agent="cursor"
        model="claude-opus-5-5"
        observedModels={['xai/grok-4.7', 'claude-opus-5-5']}
        tokens={{}}
      />,
    );

    expect(markup).toContain('claude-opus-5-5');
    expect(markup).toContain('after xai/grok-4.7');
  });

  it('uses the truncating title style in the inspector header', () => {
    const markup = renderToStaticMarkup(
      <TooltipProvider>
        <SessionInspectorTitle detail={session} showMachineBadge={false} />
      </TooltipProvider>,
    );

    expect(markup).toContain('block min-w-0 truncate text-sm font-bold');
  });

  it('supports bounded keyboard resizing for the inspector separator', () => {
    expect(inspectorWidthForKey('ArrowLeft', 440, 1000)).toBe(456);
    expect(inspectorWidthForKey('ArrowRight', 360, 1000)).toBe(360);
    expect(inspectorWidthForKey('Home', 600, 1000)).toBe(360);
    expect(inspectorWidthForKey('End', 400, 1000)).toBe(800);
    expect(inspectorWidthForKey('Enter', 440, 1000)).toBeNull();
  });

  it('retains the bounded remote session and labels exact diffs unavailable', () => {
    expect(detailPresentation(session)).toEqual({ detail: session, summaryOnly: true });
    const markup = renderToStaticMarkup(<SummaryOnlyBanner />);
    expect(markup).toContain('Showing the bounded summary from the session library');
    expect(markup).toContain('exact file diffs are disabled');
  });

  it('uses current machine health for the cached-detail warning', () => {
    expect(cachedOfflineWarning(false, 'stale')).toBe(true);
    expect(cachedOfflineWarning(true, 'ok')).toBe(false);
    expect(cachedOfflineWarning(true, undefined)).toBe(true);
  });

  it('keeps a loaded diff open only for the selected exact revision', () => {
    expect(filePanelOpen(selection, { ...session, detailRevision: 'revision-1' })).toBe(true);
    expect(filePanelOpen(selection, { ...session, detailRevision: 'revision-2' })).toBe(false);
  });

  it('retains the loaded snapshot as the session list advances', () => {
    const current = { ...session, detailRevision: 'revision-2', status: 'busy' } as Session;
    const displayed = { ...current, detailRevision: 'revision-1' };
    expect(detailRequestKey(current)).toBe(detailRequestKey(displayed));
    expect(snapshotMayBeStale(displayed, current)).toBe(true);
    expect(filePanelOpen(selection, displayed)).toBe(true);
    expect(filePanelOpen(selection, current)).toBe(false);
    expect(snapshotMayBeStale({ ...displayed, status: null }, { ...displayed, status: null })).toBe(false);
  });

  it('starts a separate synthesis attempt when the local revision advances', () => {
    const oldRevision = { ...session, sourceId: 'local', detailRevision: 'revision-1' } as Session;
    const newRevision = { ...oldRevision, detailRevision: 'revision-2' };
    expect(detailRequestKey(oldRevision)).toBe(detailRequestKey(newRevision));
    expect(synthesisAttemptKey(oldRevision)).not.toBe(synthesisAttemptKey(newRevision));
    expect(synthesisAttemptKey({ ...oldRevision, status: 'busy' })).toBe(synthesisAttemptKey(oldRevision));
    expect(synthesisAttemptKey(oldRevision)).toBe(synthesisAttemptKey({ ...oldRevision }));
    expect(synthesisMatchesSnapshot({ revision: 1000 }, 1000)).toBe(true);
    expect(synthesisMatchesSnapshot({ revision: 2000 }, 1000)).toBe(false);
  });

  it('retains detail but exposes a failed refresh attempt and its retry', () => {
    const key = detailRequestKey({ ...session, detailRevision: 'revision-1' } as Session);
    const loaded = { key, retryToken: 0 };
    expect(detailAttemptState(loaded, null, key, 1)).toEqual({
      hasSnapshot: true,
      isLoading: true,
      error: null,
    });
    const failure = { key, retryToken: 1, kind: 'other' as const, message: 'Network failed' };
    expect(detailAttemptState(loaded, failure, key, 1)).toEqual({
      hasSnapshot: true,
      isLoading: false,
      error: failure,
    });
    expect(detailAttemptState(loaded, failure, key, 2)).toEqual({
      hasSnapshot: true,
      isLoading: true,
      error: null,
    });
    const authentication = { key, retryToken: 1, kind: 'authentication' as const, message: 'Link expired' };
    expect(detailAttemptState(loaded, authentication, key, 1)).toEqual({
      hasSnapshot: false,
      isLoading: false,
      error: authentication,
    });
    expect(detailAttemptState({ key, retryToken: 2 }, null, key, 2).isLoading).toBe(false);
    expect(renderToStaticMarkup(<SnapshotRefreshStatus isLoading error={null} />)).toContain(
      'Refreshing snapshot',
    );
    const markup = renderToStaticMarkup(
      <SnapshotRefreshStatus isLoading={false} error="Network failed" onRetry={() => {}} />,
    );
    expect(markup).toContain('role="alert"');
    expect(markup).toContain('Network failed');
    expect(markup).toContain('Retry details');
    for (const kind of ['missing', 'corrupt'] as const) {
      const recovery = renderToStaticMarkup(
        <SnapshotRefreshStatus
          isLoading={false}
          error="Unavailable"
          kind={kind}
          onRetry={() => {}}
          onRefresh={() => {}}
        />,
      );
      expect(recovery).toContain('Refresh sessions');
      expect(recovery).not.toContain('Retry details');
    }
  });

  it('labels the running snapshot notice for assistive technology', () => {
    const markup = renderToStaticMarkup(<SnapshotStalenessNotice />);
    expect(markup).toContain('Session data may be stale');
    expect(markup).toContain('tabindex="0"');
  });

  it('overlays current list readiness without replacing exact detail content', () => {
    const loaded = {
      ...session,
      sourceId: 'local',
      mtime: 100,
      detailRevision: 'revision-1',
      status: 'busy',
      commands: ['exact command'],
      commits: ['old commit'],
      git: { baseBranch: 'main', ahead: 0, behind: 1 },
      lastEditAt: 10,
      launchable: false,
      subagents: [],
    } as Session;
    const current = {
      ...loaded,
      mtime: 200,
      detailRevision: 'revision-2',
      status: null,
      commands: [],
      commits: ['new commit'],
      git: { baseBranch: 'main', ahead: 1, behind: 0 },
      lastEditAt: 20,
      launchable: true,
    };

    expect(overlayLiveSessionFields(loaded, current)).toMatchObject({
      status: null,
      mtime: 200,
      commands: ['exact command'],
      commits: ['new commit'],
      git: { baseBranch: 'main', ahead: 1, behind: 0 },
      lastEditAt: 20,
      launchable: true,
    });

    expect(overlayLiveSessionFields(current, loaded)).toMatchObject({
      status: null,
      mtime: 200,
      commits: ['new commit'],
      git: { baseBranch: 'main', ahead: 1, behind: 0 },
      lastEditAt: 20,
      launchable: true,
    });
  });

  it('renders the refresh path for a structured stale diff failure', () => {
    const markup = renderToStaticMarkup(
      <DiffList changes={null} isLoading={false} loadError="stale" showRefresh onRefresh={() => {}} />,
    );
    expect(markup).toContain('stale');
    expect(markup).toContain('Refresh snapshot');
  });

  it('waits for source recovery before retrying details', async () => {
    let finishRefresh: () => void = () => {};
    const pending = new Promise<void>((resolve) => {
      finishRefresh = resolve;
    });
    const calls: string[] = [];
    const refresh = refreshSourceAndRetry(
      () => {
        calls.push('source');
        return pending;
      },
      () => calls.push('details'),
      () => true,
    );
    expect(calls).toEqual(['source']);
    finishRefresh();
    await refresh;
    expect(calls).toEqual(['source', 'details']);

    const failure = new Error('source failed');
    await expect(
      refreshSourceAndRetry(
        () => Promise.reject(failure),
        () => calls.push('details'),
        () => true,
      ),
    ).rejects.toBe(failure);
    expect(calls).toEqual(['source', 'details']);
  });

  it('ignores a source refresh completed after its inspector selection was superseded', async () => {
    let finishRefresh: () => void = () => {};
    let selectedSession = 'A';
    const pending = new Promise<void>((resolve) => {
      finishRefresh = resolve;
    });
    const calls: string[] = [];
    const refresh = refreshSourceAndRetry(
      () => pending,
      () => calls.push('retry A'),
      () => selectedSession === 'A',
    );
    selectedSession = 'B';
    finishRefresh();
    await refresh;
    expect(calls).toEqual([]);
  });

  it('renders a direct retry path for generic diff failures', () => {
    const generic = renderToStaticMarkup(
      <DiffList changes={null} isLoading={false} loadError="network failed" showRetry onRetry={() => {}} />,
    );
    const authentication = renderToStaticMarkup(
      <DiffList changes={null} isLoading={false} loadError="link expired" />,
    );

    expect(generic).toContain('Retry file changes');
    expect(authentication).not.toContain('Retry file changes');
  });

  it('offers direct recovery only for retryable generic detail failures', () => {
    const generic = renderToStaticMarkup(
      <DetailLoadError message="network failed" kind="other" onRetry={() => {}} onRefresh={() => {}} />,
    );
    const authentication = renderToStaticMarkup(
      <DetailLoadError
        message="link expired"
        kind="authentication"
        onRetry={() => {}}
        onRefresh={() => {}}
      />,
    );

    expect(generic).toContain('Retry details');
    expect(authentication).not.toContain('Retry details');
    expect(authentication).not.toContain('Refresh sessions');
  });
});
