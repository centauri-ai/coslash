import { type ComponentProps } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { machineRetryable } from '@/pages/coslash/lib/machine-status';
import { type MachineFact } from '@/pages/coslash/lib/machines';
import { buildReviewIndex } from '@/pages/coslash/lib/review';
import { boardStatusKey, getVendor, STATUS_ORDER, STATUSES, type Session } from '@/pages/coslash/lib/session';
import {
  DEFAULT_SESSION_VIEW_PREFERENCES,
  type SessionRange,
  type SessionSort,
  type SessionViewPreferences,
} from '@/pages/coslash/lib/session-view-preferences';

const groupQuery = vi.hoisted(() => ({ value: null as string | null }));
vi.mock('react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react')>();
  return {
    ...actual,
    useState: (initialState?: unknown) =>
      actual.useState(initialState === '' && groupQuery.value != null ? groupQuery.value : initialState),
  };
});

const props: ComponentProps<typeof CoslashLayout> = {
  sessions: [],
  reviewIndex: buildReviewIndex([]),
  latestHandoffs: new Map(),
  onOpenHandoffTarget: () => {},
  machines: [
    { sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true },
    { sourceId: 'remote', label: 'agent-box', state: 'ok', complete: true },
  ],
  range: 'all',
  onRangeChange: () => {},
  onViewChange: () => {},
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
  remoteReviewerOptions: [],
  canRetryRemoteReviewers: false,
  onRetryRemoteReviewers: () => {},
  onReviewStarted: () => {},
};

function renderLayout(overrides: Partial<ComponentProps<typeof CoslashLayout>> = {}) {
  const sessions = overrides.sessions ?? props.sessions;
  return renderToStaticMarkup(
    <CoslashLayout {...props} {...overrides} reviewIndex={buildReviewIndex(sessions)} />,
  );
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

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  groupQuery.value = null;
});

/** The tone class alone, so `bg-success` does not match the `bg-success-bg` wash. */
function dotFor(markup: string, tone: 'success' | 'warning' | 'danger'): string {
  const at = markup.search(new RegExp(`bg-${tone}(?![\\w-])`));
  if (at === -1) throw new Error(`no bg-${tone} dot in markup`);
  return markup.slice(at);
}

function orderOf(markup: string, ...titles: string[]): number[] {
  return titles.map((title) => markup.indexOf(title));
}

describe('CoslashLayout sidebar facet counts', () => {
  const start = new Date(2026, 1, 2).getTime();
  const locationData: [id: string, label: string, kind: string, repo: string | null, cwd: string][] = [
    ['repo:github.com/owner-a/app', 'app', 'Repository', 'github.com/owner-a/app', ''],
    ['repo:github.com/owner-b/app', 'app', 'Repository', 'github.com/owner-b/app', ''],
    ['folder:local:/work/client/app', 'client/app', 'Folder', 'app', '/work/client/app/src'],
    ['folder:local:/work/server/app', 'server/app', 'Folder', 'app', '/work/server/app/src'],
    ['folder:remote:/work/client/app', 'client/app', 'Folder', 'app', '/work/client/app/src'],
    ['folder:remote:/home/user/Foo', 'Foo', 'Folder', null, '/home/user/Foo'],
    ['folder:remote:/home/user/foo', 'foo', 'Folder', null, '/home/user/foo'],
    ['unlocated', 'No location', 'No location', null, ''],
  ];
  const locations = locationData.map(([id, label, kind, repo, cwd]) => ({ id, label, kind, repo, cwd }));
  const entry = (locationIndex: number, overrides: Partial<Session>) => {
    const location = locations[locationIndex];
    const row = session({
      id: `row-${locationIndex}`,
      name: 'needle',
      mtime: start,
      digest: [],
      repo: location.repo,
      repoLocalOnly: location.kind === 'Folder' && location.repo != null,
      cwd: location.cwd,
      ...overrides,
    });
    row.sourceLabel = row.sourceId === 'local' ? 'Local Mac' : 'agent-box';
    return { row, group: location.id };
  };
  const repeated = entry(0, { id: 'shared', status: 'busy', mtime: 0 });
  const entries = [
    repeated,
    entry(1, { id: 'shared', sourceId: 'local', status: 'waiting', mtime: 0 }),
    entry(0, { id: 'shared', agent: 'claude', status: 'idle', mtime: 0 }),
    entry(1, { status: null }),
    entry(2, { sourceId: 'local', agent: 'claude', status: 'busy', mtime: 0 }),
    entry(3, { sourceId: 'local', mtime: start - 1 }),
    entry(4, { agent: 'claude', status: 'waiting', mtime: 0 }),
    entry(5, { status: 'busy', displayStale: true, mtime: 0 }),
    entry(6, { agent: 'claude', status: 'unknown', mtime: 0 }),
    entry(7, { sourceId: 'local', name: null, firstPrompt: 'private-needle' }),
    entry(7, { agent: 'claude', name: null, firstPrompt: 'private-needle' }),
    entry(7, { agent: 'opencode', mtime: start - 1 }),
    entry(0, { id: 'non-match', name: 'other', status: 'busy', mtime: 0 }),
    entry(1, { id: 'unrecognized-status', status: 'future-status', mtime: 0 }),
    repeated,
  ];
  const machines = [
    ...props.machines,
    { sourceId: 'empty', label: 'Empty host', state: 'ok', complete: true } as MachineFact,
  ];
  const dimensions = ['status', 'machine', 'agent', 'group'] as const;
  type Dimension = (typeof dimensions)[number];
  const options: { dimension: Dimension; id: string; label: string }[] = [
    ...STATUS_ORDER.map((id) => ({ dimension: 'status' as const, id, label: STATUSES[id].label })),
    ...machines.map((machine) => ({
      dimension: 'machine' as const,
      id: machine.sourceId,
      label: machine.label,
    })),
    ...['claude', 'codex', 'opencode'].map((id) => ({
      dimension: 'agent' as const,
      id,
      label: getVendor(id).label,
    })),
    ...['Repository', 'Folder', 'No location'].flatMap((kind) =>
      locations
        .filter((location) => location.kind === kind)
        .sort((left, right) => left.label.localeCompare(right.label))
        .map((location) => ({ dimension: 'group' as const, id: location.id, label: location.label })),
    ),
  ];

  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 1, 2, 12));
  });

  function referenceRows(preferences: SessionViewPreferences, range: SessionRange) {
    const filters: Record<Dimension, readonly string[]> = {
      status: preferences.statusFilters,
      machine: preferences.machineFilters,
      agent: preferences.agentFilters,
      group: preferences.groupFilters,
    };
    return options.map((option) => ({
      label: option.label,
      selected: filters[option.dimension].includes(option.id),
      count: entries.filter(({ row, group }) => {
        if (range !== 'all' && row.status == null && row.mtime < start) return false;
        const keys = { status: boardStatusKey(row), machine: row.sourceId, agent: row.agent, group };
        const query = preferences.query;
        const location = locations.find((candidate) => candidate.id === group)!;
        let matches =
          query === '' ||
          row.name?.includes(query) ||
          (row.sourceId === 'local' && row.firstPrompt?.includes(query));
        if (query === 'group:foo') matches = location.label.toLowerCase().includes('foo');
        if (query === 'repo:owner-a') matches = row.repo?.includes('owner-a');
        if (query === 'machine:agent-box') matches = row.sourceLabel === 'agent-box';
        if (query === 'agent:codex') matches = row.agent === 'codex';
        if (query === 'status:inactive') matches = keys.status === 'inactive';
        return (
          matches &&
          keys[option.dimension] === option.id &&
          dimensions.every(
            (dimension) =>
              dimension === option.dimension ||
              filters[dimension].length === 0 ||
              filters[dimension].includes(keys[dimension]),
          )
        );
      }).length,
    }));
  }

  function sidebarRows(markup: string) {
    const sidebar = markup.slice(
      markup.indexOf('id="coslash-status"'),
      markup.indexOf('class="coslash-main'),
    );
    expect(sidebar).not.toMatch(/<button[^>]*disabled/);
    return [
      ...sidebar.matchAll(
        /aria-pressed="(true|false)"[^>]*>.*?<span class="truncate">([^<]+)<\/span><\/button>.*?<span class="text-meta ml-auto tabular-nums">(\d+)<\/span>/g,
      ),
    ].map((match) => ({ label: match[2], count: Number(match[3]), selected: match[1] === 'true' }));
  }

  function renderFacets(patch: Partial<SessionViewPreferences> = {}, range: SessionRange = 'today') {
    const preferences = { ...DEFAULT_SESSION_VIEW_PREFERENCES, ...patch };
    vi.stubGlobal('sessionStorage', { getItem: () => JSON.stringify(preferences), setItem: () => {} });
    return {
      preferences,
      markup: renderLayout({ sessions: entries.map(({ row }) => row), machines, range }),
    };
  }

  it.each(Array.from({ length: 16 }, (_, mask) => mask))(
    'matches per-option scans with selection mask %i, including OR selections and search',
    (mask) => {
      for (const multi of [false, true]) {
        for (const query of ['', 'needle']) {
          const { preferences, markup } = renderFacets({
            query,
            statusFilters: mask & 1 ? (multi ? ['busy', 'inactive'] : ['busy']) : [],
            machineFilters: mask & 2 ? (multi ? ['local', 'remote'] : ['remote']) : [],
            agentFilters: mask & 4 ? (multi ? ['claude', 'codex'] : ['codex']) : [],
            groupFilters: mask & 8 ? (multi ? [locations[0].id, 'unlocated'] : [locations[0].id]) : [],
          });
          expect(sidebarRows(markup)).toEqual(referenceRows(preferences, 'today'));
        }
      }
    },
  );

  it.each([
    { statusFilters: ['waiting'] },
    { machineFilters: ['missing'] },
    { agentFilters: ['missing'] },
    { groupFilters: ['missing'] },
    {
      statusFilters: ['busy'],
      machineFilters: ['local'],
      agentFilters: ['codex'],
      groupFilters: [locations[0].id],
    },
  ] satisfies Partial<SessionViewPreferences>[])('keeps zero-count options selectable for %j', (patch) => {
    const { preferences, markup } = renderFacets(patch);
    expect(sidebarRows(markup)).toEqual(referenceRows(preferences, 'today'));
  });

  it.each([
    'private-needle',
    'group:foo',
    'repo:owner-a',
    'machine:agent-box',
    'agent:codex',
    'status:inactive',
    'absent',
  ])('preserves privacy-aware and prefixed search counts for %s', (query) => {
    const { preferences, markup } = renderFacets({ query });
    expect(sidebarRows(markup)).toEqual(referenceRows(preferences, 'today'));
  });

  it('preserves the range boundary, raw live status retention, normalized status and repeated rows', () => {
    const today = renderFacets();
    expect(sidebarRows(today.markup)).toEqual(referenceRows(today.preferences, 'today'));
    expect(
      sidebarRows(today.markup)
        .slice(0, 5)
        .map(({ count }) => count),
    ).toEqual([4, 2, 1, 1, 5]);
    const all = renderFacets({}, 'all');
    expect(sidebarRows(all.markup)).toEqual(referenceRows(all.preferences, 'all'));
  });

  it('reuses the cached search and group results for repeated composite session keys', () => {
    const sessions = [
      entry(0, { id: 'same', name: 'needle', status: 'busy' }).row,
      entry(1, { id: 'same', name: 'other', status: 'waiting' }).row,
    ];
    for (const query of ['needle', 'other']) {
      vi.stubGlobal('sessionStorage', { getItem: () => JSON.stringify({ query }), setItem: () => {} });
      const rows = sidebarRows(renderLayout({ sessions }));
      expect(rows.map(({ count }) => count)).toEqual(
        query === 'needle' ? [0, 0, 0, 0, 0, 0, 0, 0, 0] : [1, 1, 0, 0, 0, 0, 2, 2, 2],
      );
      expect(rows.map(({ label }) => label)).toEqual([
        ...STATUS_ORDER.map((status) => STATUSES[status].label),
        'Local Mac',
        'agent-box',
        'Codex',
        'app',
      ]);
    }
  });

  it('uses the group query only to present options, not to scope counts or sessions', () => {
    groupQuery.value = 'owner-a';
    const { preferences, markup } = renderFacets({ groupFilters: [locations[1].id] });
    expect(sidebarRows(markup)).toEqual(
      referenceRows(preferences, 'today').filter(
        (_, index) => options[index].dimension !== 'group' || options[index].id === locations[0].id,
      ),
    );
    expect(markup).toContain('3 sessions');
  });
});

describe('CoslashLayout', () => {
  it('shows the monthly insights view with existing session data', () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 7, 15));
    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ view: 'insights' }),
      setItem: () => {},
    });

    const markup = renderLayout({
      sessions: [session({ id: 'one', mtime: new Date(2026, 7, 5).getTime() })],
    });

    expect(markup).toContain('August 2026');
    expect(markup).toContain('Agents used');
    expect(markup).toContain('Models used');
    expect(markup).toContain('Top repositories');
    expect(markup).toContain('Lifetime estimated cost');
    expect(markup).toContain('Sessions last active per day');
    expect(markup).toContain('aria-label="View"');
  });

  it('keeps first-run recovery visible in Insights only after an empty load', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ view: 'insights' }),
      setItem: () => {},
    });
    const emptyContent = <button>Re-run checks</button>;

    expect(renderLayout({ emptyContent })).toContain('Re-run checks');
    expect(renderLayout({ emptyContent, isLoading: true })).not.toContain('Re-run checks');
    expect(renderLayout({ emptyContent, loadError: 'Offline' })).not.toContain('Re-run checks');
    expect(renderLayout({ emptyContent, sessions: [session({ id: 'one' })] })).not.toContain('Re-run checks');
  });

  it('keeps row dividers in comfortable density and omits them in compact density', () => {
    const rowCells = (markup: string) => {
      const titleAt = markup.indexOf('Divider row');
      const row = markup.slice(markup.lastIndexOf('<tr', titleAt), markup.indexOf('</tr>', titleAt));
      return [...row.matchAll(/<td class="([^"]*)"/g)].map((match) => match[1]);
    };
    const sessions = [session({ id: 'divider', name: 'Divider row' })];

    const comfortable = rowCells(renderLayout({ sessions }));
    expect(comfortable).toHaveLength(6);
    expect(comfortable.every((cell) => cell.includes('border-b'))).toBe(true);

    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ density: 'compact' }),
      setItem: () => {},
    });
    const compact = rowCells(renderLayout({ sessions }));
    expect(compact).toHaveLength(6);
    expect(compact.every((cell) => !cell.includes('border-b'))).toBe(true);
  });

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
    expect(markup).toContain('max-w-full shrink-0 flex-wrap items-center gap-2');
    expect(markup).toContain('min-h-0 flex-1 overflow-auto');
  });

  it('places board grouping between search and the time filters', () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => JSON.stringify({ view: 'board' }),
      setItem: () => {},
    });
    const markup = renderLayout();

    const positions = orderOf(
      markup,
      'aria-label="View"',
      'placeholder="Search sessions',
      '>Columns<',
      '>Rows<',
      'aria-label="Time range"',
    );
    expect(positions.every((position) => position >= 0)).toBe(true);
    expect(positions).toEqual([...positions].sort((a, b) => a - b));
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

  it('keeps the fresh warning tone when the prompt cache is warm', () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_700_000_000_000);
    const markup = renderLayout({
      sessions: [
        session({
          id: 'warm-fresh',
          sourceId: 'local',
          mtime: 1_700_000_000_000,
          contextTokens: 160_000,
          contextWindow: 200_000,
          compactions: 0,
        }),
      ],
    });
    const detailAt = markup.indexOf('80% context · warm cache');
    const detailTag = markup.slice(markup.lastIndexOf('<span', detailAt), markup.indexOf('>', detailAt));

    expect(detailTag).toContain('text-danger-fg');
    expect(detailTag).not.toContain('text-success-fg');
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

  it('uses a remote working directory for folder groups without hiding repository labels', () => {
    const remote = session({
      id: 'remote-folder',
      name: 'Remote folder session',
      sourceId: 'remote',
      sourceLabel: 'SSH workspace',
      cwd: '/home/milan/codex_work/finance_benchmark',
      repo: null,
    });
    const repository = session({
      id: 'repository',
      name: 'Repository session',
      sourceId: 'remote',
      repo: 'github.com/centauri-ai/coslash',
      repoLocalOnly: false,
    });
    const markup = renderLayout({ sessions: [remote, repository], range: 'all' });

    expect(markup).toContain('>codex_work/finance_benchmark<');
    expect(markup).not.toContain('/home/milan/codex_work/finance_benchmark');
    expect(markup).toContain('>coslash<');
    expect(markup).toContain('>Folders<');

    vi.stubGlobal('sessionStorage', {
      getItem: () =>
        JSON.stringify({
          query: 'group:finance_benchmark',
          range: 'all',
        }),
      setItem: () => {},
    });
    const searched = renderLayout({ sessions: [remote, repository], range: 'all' });
    expect(searched).toContain('>Remote folder session<');
    expect(searched).not.toContain('>Repository session<');
  });

  it('keeps case-sensitive remote folder groups distinct', () => {
    const folder = (id: string, cwd: string) =>
      session({
        id,
        sourceId: 'remote',
        sourceLabel: 'SSH workspace',
        cwd,
        repo: null,
      });
    const markup = renderLayout({
      sessions: [folder('upper', '/home/milan/Foo'), folder('lower', '/home/milan/foo')],
      range: 'all',
    });

    expect(markup.match(/>Foo</g)).toHaveLength(2);
    expect(markup.match(/>foo</g)).toHaveLength(2);
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
