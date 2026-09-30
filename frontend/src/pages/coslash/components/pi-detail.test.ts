import { beforeEach, expect, it, vi } from 'vitest';
import { apiFetch } from '../lib/api';
import { handoffBrief } from '../lib/handoff';
import type { Session } from '../lib/session';
import { detailRequestKey, useSessionDetail } from './SessionInspector';

// Exercise the real hook's effects without adding a DOM test dependency.
const hooks = vi.hoisted(() => ({ index: 0, slots: [] as unknown[], pending: [] as (() => void)[] }));
vi.mock('react', async (original) => ({
  ...(await original<typeof import('react')>()),
  useState: (initial: unknown) => {
    const index = hooks.index++;
    if (!(index in hooks.slots)) hooks.slots[index] = initial;
    return [
      hooks.slots[index],
      (value: unknown) => {
        hooks.slots[index] = value;
      },
    ];
  },
  useRef: (initial: unknown) => {
    const index = hooks.index++;
    if (!(index in hooks.slots)) hooks.slots[index] = { current: initial };
    return hooks.slots[index];
  },
  useEffect: (effect: () => (() => void) | undefined, deps: unknown[]) => {
    const index = hooks.index++;
    const previous = hooks.slots[index] as { deps: unknown[]; cleanup?: () => void } | undefined;
    if (previous && deps.every((value, item) => Object.is(value, previous.deps[item]))) return;
    hooks.pending.push(() => {
      previous?.cleanup?.();
      hooks.slots[index] = { deps, cleanup: effect() };
    });
  },
}));
vi.mock('../lib/api', async (original) => ({
  ...(await original<typeof import('../lib/api')>()),
  apiFetch: vi.fn(),
}));

function renderDetail(session: Session) {
  hooks.index = 0;
  // oxlint-disable-next-line react-hooks/rules-of-hooks -- this test flushes mocked hook effects explicitly
  const result = useSessionDetail(session, 0, 1, 'unchanged');
  for (const effect of hooks.pending.splice(0)) effect();
  return result;
}

const session: Session = {
  sourceId: 'local',
  sourceLabel: 'Local Mac',
  agent: 'pi',
  id: 'same-session',
  detailRevision: 'leaf-a',
  mtime: 10,
  name: null,
  summary: null,
  firstPrompt: null,
  declaredGoal: null,
  synthesis: null,
  todos: [],
  digest: [],
  fileEdits: [],
  commits: [],
  subagents: [],
  tokens: {},
  cost: null,
  repo: null,
  branch: null,
  cwd: '/repo',
  status: 'idle',
  errors: 0,
  unpricedModels: [],
  displayStale: false,
  eligibleForAggregates: true,
  repoLocalOnly: false,
  files: 0,
  durationMs: null,
  entrypoint: null,
  synthesisPending: false,
  model: null,
  contextTokens: null,
  contextWindow: null,
  turns: 0,
  toolUses: 0,
  compactions: 0,
  commands: [],
  prs: 0,
  git: null,
  lastEditAt: null,
};

beforeEach(() => {
  hooks.index = 0;
  hooks.slots = [];
  hooks.pending = [];
  vi.mocked(apiFetch).mockReset();
});

it('reloads local Pi detail after a runtime tree switch without a transcript append and hands off the new context', async () => {
  const switched = { ...session, detailRevision: 'leaf-b' };
  const respond = (current: Session, selected: string) =>
    new Response(
      JSON.stringify({
        sourceId: current.sourceId,
        agent: current.agent,
        sessionId: current.id,
        revision: current.detailRevision,
        session: {
          ...current,
          digest: [
            {
              turn: 1,
              category: 'user',
              description: 'Branch A',
              contextDescription: 'Edited A',
              contextSelected: selected === 'a',
              active: selected === 'a',
            },
            {
              turn: 1,
              category: 'user',
              description: 'Branch B',
              contextDescription: 'Edited B',
              contextSelected: selected === 'b',
              active: selected === 'b',
            },
          ],
        },
      }),
      { status: 200 },
    );
  const responses = [respond(session, 'a'), respond(switched, 'b')];
  vi.mocked(apiFetch).mockImplementation(async (path) =>
    path.startsWith('/api/session-detail?')
      ? responses.shift()!
      : new Response(JSON.stringify({ revision: 0, synthesis: null, synthesisPending: false }), {
          status: 200,
        }),
  );
  expect(renderDetail(session).isLoading).toBe(true);
  await vi.waitFor(() => expect(renderDetail(session).detail).not.toBeNull());
  const initial = renderDetail(session).detail!;
  expect(handoffBrief(initial)).toContain('Edited A');
  expect(handoffBrief(initial)).not.toContain('Edited B');
  expect(renderDetail(switched).detail).toBeNull();
  await vi.waitFor(() => expect(renderDetail(switched).detail).not.toBeNull());
  const latest = renderDetail(switched).detail!;
  expect(latest.mtime).toBe(initial.mtime);
  expect(latest.digest[0].active).toBe(false);
  expect(latest.digest[1].active).toBe(true);
  expect(handoffBrief(latest)).toContain('Edited B');
  expect(handoffBrief(latest)).not.toContain('Edited A');
  expect(
    vi.mocked(apiFetch).mock.calls.filter(([path]) => path.startsWith('/api/session-detail?')),
  ).toHaveLength(2);
});

it('keeps Codex, Cursor and remote Pi detail snapshot keys frozen across content revisions', () => {
  for (const current of [
    { ...session, agent: 'codex' },
    { ...session, agent: 'cursor' },
    { ...session, sourceId: 'remote' },
  ]) {
    expect(detailRequestKey(current)).toBe(detailRequestKey({ ...current, detailRevision: 'next' }));
  }
  expect(detailRequestKey(session)).not.toBe(detailRequestKey({ ...session, detailRevision: 'next' }));
  expect(detailRequestKey(session)).toBe(detailRequestKey({ ...session, status: 'busy' }));
});
