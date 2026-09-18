import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { DiffList } from '@/pages/coslash/components/DiffList';
import {
  DetailLoadError,
  detailPresentation,
  filePanelOpen,
  overlayLiveSessionFields,
  SummaryOnlyBanner,
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
  it('retains the bounded remote session and labels exact diffs unavailable', () => {
    expect(detailPresentation(session)).toEqual({ detail: session, summaryOnly: true });
    const markup = renderToStaticMarkup(<SummaryOnlyBanner />);
    expect(markup).toContain('Showing the bounded summary from the session library');
    expect(markup).toContain('exact file diffs are disabled');
  });

  it('keeps a loaded diff open only for the selected exact revision', () => {
    expect(filePanelOpen(selection, { ...session, detailRevision: 'revision-1' })).toBe(true);
    expect(filePanelOpen(selection, { ...session, detailRevision: 'revision-2' })).toBe(false);
  });

  it('overlays current list readiness without replacing exact detail content', () => {
    const loaded = {
      ...session,
      sourceId: 'local',
      mtime: 100,
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
  });

  it('renders the refresh path for a structured stale diff failure', () => {
    const markup = renderToStaticMarkup(
      <DiffList changes={null} isLoading={false} loadError="stale" showRefresh onRefresh={() => {}} />,
    );
    expect(markup).toContain('stale');
    expect(markup).toContain('Refresh sessions');
  });

  it('offers direct recovery only for retryable generic detail failures', () => {
    const generic = renderToStaticMarkup(
      <DetailLoadError message="network failed" kind="other" onRetry={() => {}} onRefresh={() => {}} />,
    );
    const authentication = renderToStaticMarkup(
      <DetailLoadError message="link expired" kind="authentication" onRetry={() => {}} onRefresh={() => {}} />,
    );

    expect(generic).toContain('Retry details');
    expect(authentication).not.toContain('Retry details');
    expect(authentication).not.toContain('Refresh sessions');
  });
});
