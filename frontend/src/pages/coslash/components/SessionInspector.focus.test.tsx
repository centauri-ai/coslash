// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { TooltipProvider } from '@/components/ui/tooltip';
import { SessionInspector } from '@/pages/coslash/components/SessionInspector';
import { decodeSession } from '@/pages/coslash/hooks/use-sessions';

it.each([false, true])('preserves tabbed content and file-diff focus (docked=%s)', async (docked) => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.useFakeTimers();
  vi.stubGlobal('matchMedia', () => ({
    matches: docked,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
  const session = decodeSession({
    sourceId: 'local',
    agent: 'codex',
    id: 'focus-session',
    name: 'Focus regression',
    detailRevision: 'revision-1',
    cwd: '/synthetic',
    fileEdits: [{ path: 'hello.ts', adds: 1, dels: 0, edits: 1, changeIds: ['change-1'] }],
    skills: [{ name: 'review', content: 'Review the changes.' }],
    commands: ['npm test'],
    commits: ['abc123 layout change'],
    todos: [{ text: 'Check the layout', done: false }],
    digest: [{ turn: 1, category: 'user', description: 'Rearrange the inspector.' }],
  });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) =>
      Response.json(
        url.includes('/session-detail')
          ? { sourceId: 'local', agent: 'codex', sessionId: session.id, revision: 'revision-1', session }
          : {},
      ),
    ),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const onClose = vi.fn();
  const renderInspector = (selectedSession = session) => (
    <TooltipProvider>
      <SessionInspector
        session={selectedSession}
        sessionsVersion={0}
        synthesisSettingsKey=""
        synthesisCostVersion={null}
        machines={[]}
        onRefresh={() => {}}
        onClose={onClose}
        onHandoffStarted={() => {}}
        onOpenTarget={() => {}}
      />
    </TooltipProvider>
  );
  try {
    await act(async () => root.render(renderInspector()));
    const tabs = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
    expect(tabs.map((tab) => tab.textContent)).toEqual(['Session', 'Files · 1', 'Skills']);
    expect(tabs[0]!.getAttribute('aria-selected')).toBe('true');
    const panels = document.querySelectorAll<HTMLElement>('[role="tabpanel"]');
    const overview = panels[0]!.textContent!;
    const sections = [
      'RESUME OR HAND OFF',
      'DEBRIEF',
      'ARTIFACTS',
      'COMMANDS',
      'COMMITS',
      'TODOS',
      'TIMELINE',
    ];
    const positions = sections.map((section) => overview.indexOf(section));
    expect(positions.every((position) => position >= 0)).toBe(true);
    expect(positions).toEqual([...positions].sort((a, b) => a - b));
    expect(overview).not.toContain('hello.ts');
    expect(overview).not.toContain('Review the changes.');
    const commands = Array.from(panels[0]!.querySelectorAll('div')).find(
      (element) => element.textContent === '1 COMMANDS' && element.classList.contains('cursor-pointer'),
    );
    expect(commands).not.toBeNull();
    await act(async () => commands!.click());
    await act(async () => tabs[2]!.focus());
    expect(tabs[2]!.getAttribute('aria-selected')).toBe('true');
    expect(panels[2]!.textContent).toContain('Review the changes.');
    await act(async () => tabs[0]!.focus());
    expect(panels[0]!.querySelector('pre')?.textContent).toBe('npm test');
    await act(async () => tabs[1]!.focus());
    expect(tabs[1]!.getAttribute('aria-selected')).toBe('true');
    const trigger = document.querySelector<HTMLButtonElement>('button[title="hello.ts"]');
    expect(trigger).not.toBeNull();
    for (const close of ['button', 'escape']) {
      await act(async () => {
        trigger!.focus();
        trigger!.click();
      });
      const dialogs = document.querySelectorAll<HTMLElement>('[role="dialog"]');
      expect(dialogs).toHaveLength(2);
      const diff = dialogs[1]!;
      await act(async () => {
        if (close === 'button') diff.querySelector<HTMLButtonElement>('[data-slot="sheet-close"]')!.click();
        else
          document.activeElement!.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
          );
      });
      await act(async () => vi.advanceTimersByTimeAsync(1));
      expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1);
      expect(document.activeElement).toBe(trigger);
      expect(onClose).not.toHaveBeenCalled();
    }
    await act(async () => tabs[2]!.focus());
    expect(tabs[2]!.getAttribute('aria-selected')).toBe('true');
    await act(async () =>
      root.render(
        renderInspector(
          decodeSession({
            sourceId: 'local',
            agent: 'codex',
            id: session.id,
            name: 'Focus regression',
            detailRevision: '',
          }),
        ),
      ),
    );
    const summaryTabs = document.querySelectorAll<HTMLButtonElement>('[role="tab"]');
    expect(summaryTabs).toHaveLength(1);
    expect(summaryTabs[0]!.getAttribute('aria-selected')).toBe('true');
    await act(async () =>
      root.render(
        renderInspector(
          decodeSession({ sourceId: 'local', agent: 'codex', id: 'empty-session', detailRevision: '' }),
        ),
      ),
    );
    const remainingTabs = document.querySelectorAll('[role="tab"]');
    expect(remainingTabs).toHaveLength(1);
    expect(remainingTabs[0]!.textContent).toBe('Session');
    expect(remainingTabs[0]!.getAttribute('aria-selected')).toBe('true');
  } finally {
    await act(async () => root.unmount());
    mount.remove();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  }
});
