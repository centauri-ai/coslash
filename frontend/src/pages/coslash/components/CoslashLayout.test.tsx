import { type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { machineRetryable } from '@/pages/coslash/lib/machine-status';
import { type MachineFact } from '@/pages/coslash/lib/machines';
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
  onRetrySessions: () => {},
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

/** The tone class alone, so `bg-success` does not match the `bg-success-bg` wash. */
function dotFor(markup: string, tone: 'success' | 'warning' | 'danger'): string {
  const at = markup.search(new RegExp(`bg-${tone}(?![\\w-])`));
  if (at === -1) throw new Error(`no bg-${tone} dot in markup`);
  return markup.slice(at);
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
    expect(dotFor(markup, 'success')).toContain('aria-label="Synced no saved history over SSH.');
    expect(markup).not.toContain('lucide-activity');
    expect(markup).not.toContain('running ·');
    expect(markup).toContain('Filter groups');
    expect(markup).toContain('sticky top-[-20px] z-20');
    expect(markup).toContain('placeholder="Search sessions -- title, repo, prompt, recap"');
    expect(markup).toContain(
      'aria-label="Search session metadata and local prompts, recaps, summaries, goals, and syntheses"',
    );
    expect(markup).toContain(
      'Nothing matched session metadata or local prompts, recaps, summaries, goals and syntheses.',
    );
    expect(markup).toContain('aria-label="View"');
    expect(markup).toContain('>Table<');
    expect(markup).toContain('>Board<');
  });

  it('keeps the control strip reachable when its container is narrow or short', () => {
    const markup = renderLayout();

    expect(markup).toContain('flex flex-wrap items-center justify-between');
    expect(markup).toContain('max-w-full shrink-0');
    expect(markup).toContain('items-center gap-2 overflow-x-auto');
    expect(markup).toContain('min-h-0 flex-1 overflow-auto');
  });

  it('does not match an unnamed remote session by its first prompt', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ query: 'remote-private-prompt' }),
      setItem: () => {},
    });

    const markup = renderLayout({
      sessions: [session({ id: 'remote', firstPrompt: 'remote-private-prompt' })],
    });

    expect(markup).toContain('Nothing in this scope');
    expect(markup).not.toContain('>remote-private-prompt<');
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

  it('collapses row actions into one menu trigger', () => {
    const reviewable = renderLayout({
      sessions: [
        session({ id: 'with-cwd', sourceId: 'local', agent: 'claude', cwd: '/workspace/app' }),
      ] as Session[],
    });

    expect(reviewable).toContain('aria-label="Session actions"');
    expect(reviewable).not.toContain('>Send for review<');
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

    expect(dotFor(markup, 'warning')).toContain('aria-label="Offline.');
    expect(markup).not.toContain('role="alert"');
  });

  it('reports a reachable host whose refresh fell short as connected, not offline', () => {
    const markup = renderLayout({
      machines: [
        { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'stale',
          complete: false,
          reason: 'partial_agent_data',
          lastSuccessAtMs: 1000,
          lastCheckedAtMs: 2000,
        },
      ],
    });

    expect(dotFor(markup, 'warning')).toContain('aria-label="Connected.');
    expect(markup).not.toContain('Offline');
    expect(markup).not.toContain('role="alert"');
  });

  it('leaves the dot itself inert, since retry now lives in its tooltip', () => {
    const markup = renderLayout({
      machines: [{ sourceId: 'remote', label: 'agent-box', state: 'stale', complete: false }],
    });
    const dot = markup.indexOf('aria-label="Offline.');
    const tag = markup.lastIndexOf('<', dot);

    expect(markup.slice(tag, dot)).toContain('<span');
    expect(markup).not.toContain('Retry the connection');
  });

  it.each(['authentication_failed', 'host_key_failed', 'connection_failed'] as const)(
    'shows %s as a failed, retryable connection',
    (reason) => {
      const machine: MachineFact = {
        sourceId: 'remote',
        label: 'agent-box',
        state: 'error',
        complete: false,
        reason,
      };
      const markup = renderLayout({
        machines: [machine],
      });

      expect(dotFor(markup, 'danger')).toContain('aria-label="Connection needs attention.');
      expect(markup).toContain('role="alert"');
      expect(markup).toContain('Retry</button>');
      expect(markup).not.toContain('Open Settings');
      expect(machineRetryable(machine)).toBe(true);
    },
  );

  it('paints a truncated remote as connected, since its session list is whole', () => {
    const markup = renderLayout({
      machines: [
        {
          sourceId: 'remote',
          label: 'agent-box',
          state: 'limited',
          complete: false,
          reason: 'history_truncated',
        },
      ],
    });

    expect(dotFor(markup, 'success')).toContain(
      'aria-label="Connected. Older history was truncated, so these sessions are left out of the totals."',
    );
  });

  it.each([
    ['partial_agent_data', 'Connected, but some agent data could not be collected.'],
    ['no_supported_data', 'Connected, but no supported agent data was found.'],
  ] as const)('shows %s limited results as retryable incomplete data', (reason, status) => {
    const machine: MachineFact = {
      sourceId: 'remote',
      label: 'agent-box',
      state: 'limited',
      complete: false,
      reason,
    };
    const markup = renderLayout({ machines: [machine] });

    expect(dotFor(markup, 'warning')).toContain(`aria-label="${status}`);
    expect(markup).not.toContain('role="alert"');
    expect(machineRetryable(machine)).toBe(true);
  });

  it('banners a failed connector with its own reason', () => {
    const machine: MachineFact = {
      sourceId: 'remote',
      label: 'agent-box',
      state: 'ok',
      complete: true,
      helper: { state: 'ready', compatible: false, fallback: true, reason: 'helper_incompatible' },
    };
    const markup = renderLayout({ machines: [machine] });

    expect(markup).toContain('role="alert"');
    expect(markup).toContain('Setup failed: helper incompatible. Open Settings to retry.');
    expect(markup).toContain('Open Settings</button>');
    expect(machineRetryable(machine)).toBe(false);
  });
});
