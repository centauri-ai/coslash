import { type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { type Session } from '@/pages/coslash/lib/session';

const props: ComponentProps<typeof CoslashLayout> = {
  sessions: [],
  machines: [
    { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
    { sourceId: 'remote', label: 'agent-box', state: 'ok', complete: true },
  ],
  range: 'all',
  onRangeChange: () => {},
  selectedSessionKey: null,
  onSelectSession: () => {},
  diagnostics: null,
  onSettings: () => {},
  theme: 'light',
  onThemeChange: () => {},
  themeDisabled: false,
  onRetry: () => {},
  retrying: false,
  isLoading: false,
  loadError: null,
  reviewerOptions: [
    { id: 'claude', label: 'Claude Code', available: true },
    { id: 'codex', label: 'Codex', available: false },
    { id: 'opencode', label: 'OpenCode', available: false },
  ],
  onReviewStarted: () => {},
};

function renderLayout(overrides: Partial<ComponentProps<typeof CoslashLayout>> = {}) {
  return renderToStaticMarkup(<CoslashLayout {...props} {...overrides} />);
}

describe('CoslashLayout', () => {
  it('offers agent facets for the vendors present, not the reviewer CLIs installed', () => {
    const markup = renderLayout({
      sessions: [
        { id: 'one', sourceId: 'remote', agent: 'opencode', cwd: '/workspace/app', status: null, mtime: 0 },
      ] as Session[],
      range: 'today',
    });

    expect(markup).toContain('OpenCode');
    expect(markup).not.toContain('Claude Code');
    expect(markup).not.toContain('>Codex<');
  });

  it('renders the connection facets and the search and view controls', () => {
    const markup = renderLayout();

    expect(markup).toContain('agent-box');
    expect(markup).toContain('lucide-server');
    expect(markup).toContain('aria-label="Connected"');
    expect(markup).not.toContain('lucide-activity');
    expect(markup).not.toContain('running ·');
    expect(markup).toContain('Filter groups');
    expect(markup).toContain('sticky top-[-20px] z-20');
    expect(markup).toContain('placeholder="Search sessions -- title, repo, branch"');
    expect(markup).not.toContain('Search recorded context');
    expect(markup).toContain('aria-label="View"');
    expect(markup).toContain('>Table<');
    expect(markup).toContain('>Board<');
  });

  it('keeps sessions with truncated history out of the header totals', () => {
    const rollupSession = (overrides: Partial<Session>) =>
      ({
        sourceId: 'remote',
        agent: 'codex',
        cwd: '/workspace/app',
        status: null,
        mtime: 0,
        tokens: {
          'gpt-5': {
            input_tokens: 1000,
            output_tokens: 0,
            cache_creation_input_tokens: 0,
            cache_creation_1h_input_tokens: 0,
            cache_read_input_tokens: 0,
          },
        },
        cost: 1,
        unpricedModels: [],
        eligibleForAggregates: true,
        ...overrides,
      }) as Session;

    const markup = renderLayout({
      sessions: [
        rollupSession({ id: 'counted' }),
        rollupSession({ id: 'truncated', cost: 9, eligibleForAggregates: false }),
      ],
    });

    expect(markup).toContain('2 sessions');
    expect(markup).toContain('≈$1.00');
    expect(markup).not.toContain('$10.00');
    expect(markup).toContain('>$9.00<');
  });

  it('separates verified repositories from local-only folders', () => {
    const sessions = [
      {
        id: 'folder',
        sourceId: 'local',
        agent: 'codex',
        repo: 'centauri-ai',
        repoLocalOnly: true,
        cwd: '/workspace/folders/centauri-ai',
        status: null,
        mtime: 0,
      },
      {
        id: 'repository',
        sourceId: 'local',
        agent: 'codex',
        repo: 'github.com/centauri-ai/centauri-ai',
        repoLocalOnly: false,
        cwd: '/workspace/repos/centauri-ai',
        status: null,
        mtime: 0,
      },
      {
        id: 'folder-duplicate',
        sourceId: 'local',
        agent: 'codex',
        repo: 'centauri-ai',
        repoLocalOnly: true,
        cwd: '/workspace/other/centauri-ai',
        status: null,
        mtime: 0,
      },
    ] as Session[];

    const markup = renderLayout({ sessions, range: 'today' });
    const repositoryHeading = markup.indexOf('Repositories');
    const folderHeading = markup.indexOf('Folders');
    const groups = [...markup.matchAll(/centauri-ai/g)].map((match) => match.index);

    expect(groups).toHaveLength(2);
    expect(groups[0]).toBeGreaterThan(repositoryHeading);
    expect(groups[0]).toBeLessThan(folderHeading);
    expect(groups[1]).toBeGreaterThan(folderHeading);
    expect(markup.match(/lucide-folder(?!-git)/g)).toHaveLength(1);
    expect(markup.match(/lucide-folder-git-2/g)).toHaveLength(1);
  });

  it('shows loading indicators while refreshing the selected time window', () => {
    const markup = renderLayout({ isLoading: true });

    expect(markup).toContain('Refreshing sessions');
  });

  it('does not report a remote outage during the initial refresh', () => {
    const markup = renderLayout({
      machines: [
        { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'stale',
          complete: false,
          reason: 'initial_refresh',
          refreshing: true,
        },
      ],
    });

    expect(markup).not.toContain('unreachable');
  });

  it('reports a remote outage after a refresh fails', () => {
    const markup = renderLayout({
      machines: [
        { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'stale',
          complete: false,
          reason: 'refresh_timeout',
        },
      ],
    });

    expect(markup).toContain('agent-box');
    expect(markup).toContain('unreachable');
  });
});
