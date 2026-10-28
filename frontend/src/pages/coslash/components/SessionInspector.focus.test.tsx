// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { TooltipProvider } from '@/components/ui/tooltip';
import { SessionInspector } from '@/pages/coslash/components/SessionInspector';
import { decodeSession } from '@/pages/coslash/hooks/use-sessions';

it.each([false, true])('restores file focus after Close and Escape (docked=%s)', async (docked) => {
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
  try {
    await act(async () =>
      root.render(
        <TooltipProvider>
          <SessionInspector
            session={session}
            sessionsVersion={0}
            synthesisSettingsKey=""
            synthesisCostVersion={null}
            machines={[]}
            onRefresh={() => {}}
            onClose={onClose}
            onHandoffStarted={() => {}}
            onOpenTarget={() => {}}
          />
        </TooltipProvider>,
      ),
    );
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
  } finally {
    await act(async () => root.unmount());
    mount.remove();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  }
});
