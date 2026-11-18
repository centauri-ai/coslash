// @vitest-environment jsdom
import { act, type ComponentProps } from 'react';
import { createRoot } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import axe from 'axe-core';
import { describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from '@/pages/coslash/components/CoslashLayout';
import { DebriefProse } from '@/pages/coslash/components/SessionInspector';
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
