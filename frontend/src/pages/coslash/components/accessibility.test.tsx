// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import axe from 'axe-core';
import { describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { DebriefProse } from '@/pages/coslash/components/session-inspector/RecapSection';
import { SettingsDialog } from '@/pages/coslash/components/SettingsDialog';
import { buildReviewIndex } from '@/pages/coslash/lib/review';
import { makeSession } from '@/pages/coslash/lib/session-fixture';
import { type SettingsResponse } from '@/pages/coslash/lib/settings';

const layoutProps: ComponentProps<typeof CoslashLayout> = {
  sessions: [],
  reviewIndex: buildReviewIndex([]),
  latestHandoffs: new Map(),
  onOpenHandoffTarget: () => {},
  machines: [{ sourceId: 'local', label: 'Local Mac', state: 'ok', complete: true }],
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
  synthesisCostVersion: null,
  reviewerOptions: [],
  remoteReviewerOptions: [],
  canRetryRemoteReviewers: false,
  onRetryRemoteReviewers: () => {},
  onReviewStarted: () => {},
};

const settingsResponse: SettingsResponse = {
  settingsPath: '/custom/coslash/settings.json',
  settings: {
    $schema: 'x',
    version: 1,
    synthesis: { enabled: false, backend: '', model: '' },
    appearance: { theme: 'light' },
    launch: { terminal: 'Terminal' },
  },
  persisted: true,
  valid: true,
  options: { synthesisBackends: [], terminals: [], reviewers: [] },
};

async function expectAxeClean(context: Element | Document) {
  const result = await axe.run(context, {
    runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
    rules: { 'color-contrast': { enabled: false } },
  });
  expect(result.violations.map(({ id, help }) => ({ id, help }))).toEqual([]);
}

describe('coSlash accessibility', () => {
  it('passes axe checks on the main session table and Settings dialog', async () => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT =
      true;
    const session = makeSession({
      id: 'axe-table',
      name: 'Accessible row',
      hubSyncChip: 'parked_failed',
      hubSyncFailureCode: 'unreadable_source',
      cost: 0,
    });
    document.body.innerHTML = renderToStaticMarkup(
      <CoslashLayout {...layoutProps} sessions={[session]} reviewIndex={buildReviewIndex([session])} />,
    );
    await expectAxeClean(document.body);

    document.body.replaceChildren();
    const mount = document.createElement('div');
    document.body.append(mount);
    const root = createRoot(mount);
    await act(async () => {
      root.render(
        <SettingsDialog
          open
          mode="full-settings"
          onOpenChange={() => {}}
          response={settingsResponse}
          isLoading={false}
          loadError={null}
          saveError={null}
          isSaving={false}
          onSave={async () => false}
          syncStatusLoadState="ready"
        />,
      );
    });
    expect(document.body.textContent).toContain('/custom/coslash/settings.json');
    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    await expectAxeClean(dialog!);
    await act(async () => root.unmount());
  });

  it('browses nested worktrees independently of the selected checkout and restores expansion after search', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    const repo = 'github.com/acme/app';
    const linked = {
      id: 'link',
      cloneId: 'clone',
      label: 'fix-sidebar',
      cwd: '/ws/fix-sidebar',
      root: '/ws/fix-sidebar',
      repo,
      main: false,
      lastSeen: 2,
      headKind: 'branch' as const,
      availability: 'available' as const,
    };
    const sessions = [
      makeSession({
        id: 'link',
        name: 'Linked session',
        cwd: linked.cwd,
        locations: [linked, { ...linked, label: 'old-name', root: '/ws/old-name', lastSeen: 1 }],
      }),
      makeSession({
        id: 'main',
        name: 'Main session',
        cwd: '/ws/main',
        locations: [{ ...linked, id: 'main', cwd: '/ws/main', root: '/ws/main', main: true }],
      }),
      makeSession({ id: 'unknown', name: 'Legacy session', cwd: '/ws/unknown', repo }),
      makeSession({
        id: 'parentless',
        name: 'Parentless session',
        cwd: '/ws/solo',
        locations: [{ ...linked, id: 'solo', label: 'solo', cwd: '/ws/solo', root: '/ws/solo', repo: '' }],
      }),
      makeSession({
        id: 'only-child',
        name: 'Only child session',
        cwd: '/ws/child',
        locations: [
          {
            ...linked,
            id: 'child',
            label: 'child',
            cwd: '/ws/child',
            root: '/ws/child',
            repo: 'github.com/acme/only-linked',
          },
        ],
      }),
    ];
    const mount = document.createElement('div');
    document.body.replaceChildren(mount);
    const root = createRoot(mount);
    try {
      await act(async () =>
        root.render(
          <CoslashLayout {...layoutProps} sessions={sessions} reviewIndex={buildReviewIndex(sessions)} />,
        ),
      );
      const sidebar = mount.querySelector('[aria-label="Filters"]')!;
      const option = (name: string) =>
        Array.from(sidebar.querySelectorAll('button')).find((el) => el.textContent === name);
      const toggle = () => sidebar.querySelector<HTMLButtonElement>('[aria-label="Worktrees in app"]')!;
      const visibleTitles = () =>
        Array.from(mount.querySelectorAll('tbody button')).map((el) => el.textContent);
      const input = sidebar.querySelector<HTMLInputElement>('[aria-label="Find a location"]')!;
      const search = (value: string) =>
        act(async () => {
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
          input.dispatchEvent(new Event('input', { bubbles: true }));
        });
      expect(toggle().getAttribute('aria-expanded')).toBe('false');
      expect(option('only-linked')?.parentElement?.lastElementChild?.textContent).toBe('0');
      expect(option('app')?.parentElement?.lastElementChild?.textContent).toBe('1');
      expect(option('fix-sidebar')).toBeUndefined();
      expect(option('/ws/unknown')).toBeDefined();
      await act(async () => option('app')!.click());
      expect(visibleTitles()).toContain('Main session');
      expect(visibleTitles()).not.toContain('Legacy session');
      expect(visibleTitles()).not.toContain('Linked session');
      await search('old-name');
      expect(toggle().disabled).toBe(true);
      expect(option('fix-sidebar')).toBeDefined();
      expect(visibleTitles()).toContain('Main session');
      await act(async () => option('fix-sidebar')!.click());
      expect(visibleTitles()).toContain('Linked session');
      expect(visibleTitles()).not.toContain('Main session');
      await search('');
      expect(toggle().getAttribute('aria-expanded')).toBe('false');
      await act(async () => toggle().click());
      expect(visibleTitles()).toContain('Linked session');
      await search('only-linked');
      expect(option('child')).toBeDefined();
      await search('');
      expect(toggle().getAttribute('aria-expanded')).toBe('true');
      const other = [...sidebar.querySelectorAll<HTMLElement>('[tabindex="0"]')].find(
        (el) => el.textContent === 'Other worktrees',
      )!;
      expect(other.classList.contains('cursor-default')).toBe(true);
      await act(async () => other.focus());
      expect(document.querySelector('[role="tooltip"]')?.textContent).toContain(
        'parent repository could not be identified',
      );
      await act(async () =>
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
      );
      expect(document.querySelector('[role="tooltip"]')).toBeNull();
    } finally {
      await act(async () => root.unmount());
      sessionStorage.clear();
      vi.unstubAllGlobals();
    }
  });

  it('collapses the Folders category and expands it while a location search is active', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        unobserve() {}
        disconnect() {}
      },
    );
    const sessions = [makeSession({ id: 'notes', cwd: '/ws/notes' })];
    const mount = document.createElement('div');
    document.body.replaceChildren(mount);
    const root = createRoot(mount);
    try {
      await act(async () =>
        root.render(
          <CoslashLayout {...layoutProps} sessions={sessions} reviewIndex={buildReviewIndex(sessions)} />,
        ),
      );
      const sidebar = mount.querySelector('[aria-label="Filters"]')!;
      const toggle = () =>
        [...sidebar.querySelectorAll<HTMLButtonElement>('h3 button')].find(
          (el) => el.textContent === 'Folders',
        )!;
      const folderRow = () => [...sidebar.querySelectorAll('button')].find((el) => el.title === '/ws/notes');
      const input = sidebar.querySelector<HTMLInputElement>('[aria-label="Find a location"]')!;
      const search = (value: string) =>
        act(async () => {
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value);
          input.dispatchEvent(new Event('input', { bubbles: true }));
        });
      expect(sidebar.classList.contains('w-[214px]')).toBe(true);
      expect(toggle().classList.contains('w-full')).toBe(true);
      expect(toggle().getAttribute('aria-expanded')).toBe('true');
      expect(folderRow()).toBeDefined();
      await act(async () => toggle().click());
      expect(toggle().getAttribute('aria-expanded')).toBe('false');
      expect(folderRow()).toBeUndefined();
      await search('notes');
      expect(toggle().disabled).toBe(true);
      expect(folderRow()).toBeDefined();
      await search('');
      expect(folderRow()).toBeUndefined();
    } finally {
      await act(async () => root.unmount());
      sessionStorage.clear();
      vi.unstubAllGlobals();
    }
  });

  it.each(['list', 'board'])('selects a session once per title click in the %s view', async (view) => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT =
      true;
    sessionStorage.setItem('coslash.session-view-preferences.v2', JSON.stringify({ view }));
    const session = makeSession({ id: 'click', name: 'Click target' });
    const onSelectSession = vi.fn();
    const mount = document.createElement('div');
    document.body.replaceChildren(mount);
    const root = createRoot(mount);
    await act(async () =>
      root.render(
        <CoslashLayout
          {...layoutProps}
          sessions={[session]}
          reviewIndex={buildReviewIndex([session])}
          onSelectSession={onSelectSession}
        />,
      ),
    );
    await act(async () => import('@/pages/coslash/components/SessionBoard'));
    const title = [...mount.querySelectorAll('button')].find(
      (button) => button.textContent === 'Click target',
    );
    await act(async () => title!.click());

    expect(onSelectSession).toHaveBeenCalledTimes(1);
    await act(async () => root.unmount());
    sessionStorage.clear();
  });

  it('exposes the debrief show-more toggle as an expandable button', async () => {
    (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT =
      true;
    const blocks = ['One.', 'Two.', 'Three.', 'Four.', 'Five.'].map((text) => ({
      kind: 'paragraph' as const,
      text,
    }));
    const mount = document.createElement('div');
    document.body.replaceChildren(mount);
    const root = createRoot(mount);
    await act(async () => root.render(<DebriefProse blocks={blocks} tone="goal" />));
    const toggle = mount.querySelector('button[aria-expanded]')!;

    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    await act(async () => (toggle as HTMLButtonElement).click());
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(mount.textContent).toContain('Five.');
    await act(async () => root.unmount());
  });
});
