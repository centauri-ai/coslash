import { type ComponentProps, type ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CoslashLayout } from './components/CoslashLayout';
import { DeleteSessionDialog } from './components/DeleteSessionDialog';
import { CoslashPage } from './CoslashPage';
import { sessionKey, type Session } from './lib/session';

const hooks = vi.hoisted(() => {
  let cursor = 0;
  let values: unknown[] = [];
  let dependencies: (readonly unknown[] | undefined)[] = [];
  let effects: (() => void)[] = [];
  const changed = (index: number, next?: readonly unknown[]) => {
    const prior = dependencies[index];
    return (
      prior == null ||
      next == null ||
      prior.length !== next.length ||
      prior.some((value, dependencyIndex) => !Object.is(value, next[dependencyIndex]))
    );
  };
  return {
    reset() {
      values = [];
      dependencies = [];
    },
    useState(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = typeof initial === 'function' ? initial() : initial;
      return [
        values[index],
        (next: unknown) => {
          values[index] = typeof next === 'function' ? next(values[index]) : next;
        },
      ];
    },
    useRef(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = { current: initial };
      return values[index];
    },
    useMemo(factory: () => unknown, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) values[index] = factory();
      dependencies[index] = next;
      return values[index];
    },
    useEffect(effect: () => void, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) effects.push(effect);
      dependencies[index] = next;
    },
    render<Props>(component: (props: Props) => unknown, props: Props) {
      cursor = 0;
      effects = [];
      const root = component(props);
      for (const effect of effects) effect();
      return root;
    },
  };
});

vi.mock('react', async (original) => ({
  ...(await original<typeof import('react')>()),
  useState: hooks.useState,
  useRef: hooks.useRef,
  useEffect: hooks.useEffect,
  useMemo: hooks.useMemo,
  useCallback: (callback: unknown) => callback,
}));

const data = vi.hoisted(() => ({ sessions: [] as Session[], refresh: vi.fn() }));
vi.mock('./hooks/use-sessions', () => ({
  useSessions: () => ({
    sessions: data.sessions,
    machines: [],
    isLoading: false,
    loadError: null,
    refreshSessions: data.refresh,
  }),
  useShareCandidates: () => ({ sessions: [] }),
}));
vi.mock('./hooks/use-settings', () => ({ useSettings: () => ({ response: null }) }));
vi.mock('./hooks/use-diagnostics', () => ({ useDiagnostics: () => ({ diagnostics: null }) }));
vi.mock('./features/sharing/api', () => ({ loadHubDestination: async () => null }));
const local = {
  sourceId: 'local',
  agent: 'codex',
  id: 'same',
  logicalSessionId: 'local:codex:same',
  revision: 1,
} as Session;
const otherAgent = { ...local, agent: 'claude', logicalSessionId: 'local:claude:same' };
const remote = { ...local, sourceId: 'remote', logicalSessionId: 'remote:codex:same' };
function render() {
  const root = hooks.render(CoslashPage, undefined) as ReactElement<{ children: ReactElement[] }>;
  const layout = root.props.children.find((child) => child?.type === CoslashLayout)! as ReactElement<
    ComponentProps<typeof CoslashLayout>
  >;
  const dialog = root.props.children.find((child) => child?.type === DeleteSessionDialog)! as ReactElement<
    ComponentProps<typeof DeleteSessionDialog>
  >;
  return { layout: layout.props, dialog: dialog.props };
}
beforeEach(() => {
  hooks.reset();
  vi.clearAllMocks();
  data.sessions = [local, otherAgent, remote];
  vi.stubGlobal('window', { location: { search: '' } });
  vi.stubGlobal('document', { activeElement: null });
  vi.stubGlobal('HTMLElement', class {});
});
describe('page delete flow', () => {
  it.each([otherAgent, remote])(
    'preserves a superseding selection with a colliding id: $sourceId/$agent',
    (next) => {
      render().layout.onSelectSession(local);
      render().layout.onDelete?.(local);
      render().layout.onSelectSession(next);
      render().dialog.onDeleted(local);
      expect(render().layout.selectedSessionKey).toBe(sessionKey(next));
    },
  );
  it('clears only the deleted composite identity and reloads the list', () => {
    render().layout.onSelectSession(local);
    render().layout.onDelete?.(local);
    expect(render().dialog.session).toBe(local);
    render().dialog.onRefresh();
    render().dialog.onDeleted(local);
    render().dialog.onClose();
    expect(data.refresh).toHaveBeenCalledOnce();
    expect(render().layout.selectedSessionKey).toBeNull();
    expect(render().dialog.session).toBeNull();
  });
  it('refuses a remote delete trigger', () => {
    render().layout.onDelete?.(remote);
    expect(render().dialog.session).toBeNull();
  });
});
