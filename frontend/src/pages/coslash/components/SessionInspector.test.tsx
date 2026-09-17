import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { DiffList } from '@/pages/coslash/components/DiffList';
import {
  detailPresentation,
  filePanelOpen,
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

  it('renders the refresh path for a structured stale diff failure', () => {
    const markup = renderToStaticMarkup(
      <DiffList changes={null} isLoading={false} loadError="stale" showRefresh onRefresh={() => {}} />,
    );
    expect(markup).toContain('stale');
    expect(markup).toContain('Refresh sessions');
  });
});
