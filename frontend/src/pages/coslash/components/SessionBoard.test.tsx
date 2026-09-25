import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { SessionBoard } from '@/pages/coslash/components/SessionBoard';
import type { ReviewIndex } from '@/pages/coslash/lib/review';
import type { Session } from '@/pages/coslash/lib/session';
import type { BoardGroupBy, BoardRowGroupBy } from '@/pages/coslash/lib/session-grouping';

function session(id: string, overrides: Partial<Session> = {}): Session {
  return {
    id,
    sourceId: 'remote',
    sourceLabel: 'SSH workspace',
    agent: 'codex',
    cwd: '/workspace/app',
    branch: id,
    repo: null,
    status: null,
    mtime: 0,
    tokens: {},
    cost: null,
    unpricedModels: [],
    subagents: [],
    displayStale: false,
    compactions: 0,
    name: id,
    firstPrompt: null,
    ...overrides,
  } as Session;
}

const reviewIndex: ReviewIndex<Session> = {
  links: new Map(),
  activeOrigins: new Set(),
  reviewSessions: new Set(),
};

function renderBoard(
  sessions: Session[],
  columnGroupBy: BoardGroupBy = 'branch',
  rowGroupBy: BoardRowGroupBy = 'branch',
) {
  return renderToStaticMarkup(
    <SessionBoard
      sessions={sessions}
      columnGroupBy={columnGroupBy}
      rowGroupBy={rowGroupBy}
      onSelectSession={() => {}}
      review={{
        index: reviewIndex,
        reviewerOptions: [{ id: 'claude', label: 'Claude Code', available: true }],
        remoteReviewerOptions: [],
        onStarted: () => {},
        onSelectRelated: () => {},
      }}
    />,
  );
}

describe('SessionBoard', () => {
  it('renders only non-empty intersections for high-cardinality dimensions', () => {
    const markup = renderBoard([session('one'), session('two'), session('three')]);

    expect(markup.match(/grid-column:/g)).toHaveLength(3);
  });

  it('withholds review when a local session has no workspace', () => {
    const markup = renderBoard([session('local', { sourceId: 'local', cwd: '' })]);

    expect(markup).not.toContain('Send for review');
  });

  it('shows review for a launchable remote session without exposing its working directory', () => {
    const markup = renderBoard([session('remote-review', { cwd: '', launchable: true })]);

    expect(markup).toContain('Send for review');
  });

  it('shows the full repository identity on same-named columns', () => {
    const markup = renderBoard(
      [
        session('one', { repo: 'github.com/centauri-ai/coslash' }),
        session('two', { repo: 'github.com/other/coslash' }),
      ],
      'repo',
      'none',
    );

    expect(markup).toContain('title="github.com/centauri-ai/coslash"');
    expect(markup).toContain('title="github.com/other/coslash"');
  });

  it('shows the warning dot on Inspect readiness columns', () => {
    const markup = renderBoard(
      [
        session('inspect', {
          sourceId: 'local',
          contextTokens: 124_000,
          contextWindow: 200_000,
          mtime: 0,
        }),
      ],
      'readiness',
      'none',
    );

    expect(markup).toContain('size-[7px] shrink-0 rounded-full bg-warning');
  });

  it('clamps prompt-derived titles to two lines', () => {
    const markup = renderBoard([session('one', { name: null, firstPrompt: 'A long prompt-derived title' })]);

    expect(markup).toContain('line-clamp-2');
    expect(markup).toContain('break-words');
  });
});
