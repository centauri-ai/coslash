import { type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { type Session } from '@/pages/coslash/lib/session';
import { type SessionSort } from '@/pages/coslash/lib/session-view-preferences';

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

function session(overrides: Partial<Session>): Session {
  return {
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
    displayStale: false,
    name: null,
    firstPrompt: null,
    ...overrides,
  } as Session;
}

/** The layout reads its sort from session storage, which the node test environment lacks. */
function storeSort(sort: SessionSort) {
  const stored = JSON.stringify({ sort });
  vi.stubGlobal('sessionStorage', { getItem: () => stored, setItem: () => {} });
}

afterEach(() => vi.unstubAllGlobals());

function dotFor(markup: string, colour: 'green' | 'amber' | 'clay'): string {
  return markup.slice(markup.indexOf(`bg-coslash-${colour}-dot`));
}

function orderOf(markup: string, ...titles: string[]): number[] {
  return titles.map((title) => markup.indexOf(title));
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
    expect(dotFor(markup, 'green')).toContain('aria-label="Synced no saved history over SSH.');
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
    const markup = renderLayout({
      sessions: [
        session({ id: 'counted' }),
        session({ id: 'truncated', cost: 9, eligibleForAggregates: false }),
      ],
    });

    expect(markup).toContain('2 sessions');
    expect(markup).toContain('≈$1.00');
    expect(markup).not.toContain('$10.00');
    expect(markup).toContain('>$9.00<');
  });

  it('keeps same-named repositories from different owners apart', () => {
    const sessions = [
      {
        id: 'upstream',
        sourceId: 'local',
        agent: 'codex',
        repo: 'github.com/centauri-ai/coslash',
        repoLocalOnly: false,
        cwd: '/workspace/upstream',
        status: null,
        mtime: 0,
      },
      {
        id: 'fork',
        sourceId: 'local',
        agent: 'codex',
        repo: 'github.com/calvintvu/coslash',
        repoLocalOnly: false,
        cwd: '/workspace/fork',
        status: null,
        mtime: 0,
      },
    ] as Session[];

    const markup = renderLayout({ sessions, range: 'today' });

    expect(markup.match(/>coslash</g)).toHaveLength(2);
  });

  it('sorts A-Z on the title the row actually renders', () => {
    storeSort({ key: 'title', dir: 'asc' });
    const markup = renderLayout({
      sessions: [
        session({ id: 'named', name: 'Banana' }),
        session({ id: 'prompt-z', firstPrompt: 'Zebra prompt' }),
        session({ id: 'prompt-a', firstPrompt: 'Apple prompt' }),
      ],
    });

    const [apple, banana, zebra] = orderOf(markup, 'Apple prompt', 'Banana', 'Zebra prompt');
    expect(apple).toBeLessThan(banana);
    expect(banana).toBeLessThan(zebra);
  });

  it('sorts rows with unknown liveness below current rows', () => {
    const markup = renderLayout({
      sessions: [
        session({ id: 'stale', name: 'Stale but recent', mtime: 200, displayStale: true }),
        session({ id: 'live', name: 'Live and older', mtime: 100 }),
      ],
    });

    const [live, stale] = orderOf(markup, 'Live and older', 'Stale but recent');
    expect(live).toBeLessThan(stale);
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

    expect(markup.indexOf('>centauri-ai<')).toBeGreaterThan(repositoryHeading);
    expect(markup.indexOf('>centauri-ai<')).toBeLessThan(folderHeading);
    expect(markup.match(/lucide-folder(?!-git)/g)).toHaveLength(1);
    expect(markup.match(/lucide-folder-git-2/g)).toHaveLength(1);
  });

  it('keeps same-named local folders apart by their path', () => {
    const folder = (id: string, cwd: string) =>
      ({
        id,
        sourceId: 'local',
        agent: 'codex',
        repo: 'app',
        repoLocalOnly: true,
        cwd,
        status: null,
        mtime: 0,
      }) as Session;

    const markup = renderLayout({
      sessions: [folder('client', '/work/client/app'), folder('server', '/work/server/app/src')],
      range: 'today',
    });

    expect(markup).toContain('>client/app<');
    expect(markup).toContain('>server/app<');
  });

  it('withholds review from a local session that has no working directory', () => {
    const reviewable = renderLayout({
      sessions: [
        session({ id: 'with-cwd', sourceId: 'local', agent: 'claude', cwd: '/workspace/app' }),
      ] as Session[],
    });
    const unreviewable = renderLayout({
      sessions: [session({ id: 'without-cwd', sourceId: 'local', agent: 'claude', cwd: '' })] as Session[],
    });

    expect(reviewable).toContain('Send for review');
    expect(unreviewable).not.toContain('Send for review');
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

  it('shows a stale host as degraded without interrupting with a banner', () => {
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

    expect(dotFor(markup, 'amber')).toContain('aria-label="Offline.');
    expect(markup).not.toContain('role="alert"');
  });

  it('offers the offline retry as a focusable control beside the machine filter', () => {
    const markup = renderLayout({
      machines: [{ sourceId: 'remote', label: 'agent-box', state: 'stale', complete: false }],
    });
    const retry = markup.indexOf('aria-label="Offline.');
    const tag = markup.lastIndexOf('<', retry);
    const before = markup.slice(0, tag);

    expect(markup.slice(tag, retry)).toContain('<button');
    expect(before.lastIndexOf('</button>')).toBeGreaterThan(before.lastIndexOf('<button'));
  });

  it('sends a failed connector to Settings rather than another refresh', () => {
    const markup = renderLayout({
      machines: [
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'error',
          complete: false,
          reason: 'authentication_failed',
        },
      ],
    });

    expect(markup).toContain('Open Settings');
    expect(markup).not.toContain('>Retry<');
  });

  it('does not paint a degraded remote as connected', () => {
    const markup = renderLayout({
      machines: [{ sourceId: 'remote', label: 'agent-box', state: 'limited', complete: false }],
    });

    expect(dotFor(markup, 'amber')).toContain('aria-label="Showing the available remote history."');
  });

  it('banners a failed connector with its own reason', () => {
    const markup = renderLayout({
      machines: [
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'ok',
          complete: true,
          helper: { state: 'ready', compatible: false, fallback: true, reason: 'helper_incompatible' },
        },
      ],
    });

    expect(markup).toContain('role="alert"');
    expect(markup).toContain('Setup failed: helper incompatible. Open Settings to retry.');
  });
});
