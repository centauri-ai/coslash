// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { handoffBrief } from '../lib/handoff';
import { sessionMatchesSearchTerm } from '../lib/search';
import { makeSession } from '../lib/session-fixture';
import { DigestSection } from './session-inspector/DigestSection';
import { RecapSection } from './session-inspector/RecapSection';

const evidence = { entry: 1, turn: 2, quote: 'keep the complete source history' };
const decision = {
  id: 'choice',
  text: 'Preserve history',
  why: 'Audits require continuity',
  alternative: 'Discarding branches',
  byYou: true,
  cited: true,
  evidence: [evidence],
};
const detail = makeSession({
  sourceId: 'local',
  agent: 'pi',
  id: 'choices',
  synthesis: {
    goals: [],
    outcome: '',
    nextStep: '',
    keyDecisions: ['Preserve history'],
    decisionsVersion: 'decisions-2026-11-v1',
    designDoc: 'docs/plan.md',
    decisions: [
      decision,
      { ...decision, id: 'uncited', text: 'Unverified choice', cited: false, byYou: false, evidence: [] },
    ],
  },
  digest: [
    { category: 'user', turn: 1, description: 'Starting the work' },
    { category: 'recap', turn: 2, description: 'Visible branch summary', contextDescription: evidence.quote },
  ],
});

const originalScroll = HTMLElement.prototype.scrollIntoView;
let mount: HTMLDivElement;
let root: ReturnType<typeof createRoot>;
beforeEach(() => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  mount = document.createElement('div');
  document.body.append(mount);
  root = createRoot(mount);
  HTMLElement.prototype.scrollIntoView = vi.fn();
});
afterEach(async () => {
  await act(async () => root.unmount());
  mount.remove();
  HTMLElement.prototype.scrollIntoView = originalScroll;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('renders compact decisions with the You mark first, hover reasons, turn links, and the design doc', async () => {
  const html = renderToStaticMarkup(<RecapSection detail={detail} />);
  expect(html.indexOf('YOU')).toBeLessThan(html.indexOf('Preserve history'));
  expect(html).not.toContain('Audits require continuity');
  expect(html).not.toContain('aria-expanded');
  expect(html).toContain('title="docs/plan.md">plan.md<');
  expect(html).toContain('more decisions in the doc');
  expect((html.match(/turn 2</g) ?? []).length).toBe(1);

  const onEvidence = vi.fn();
  const onOpenDesignDoc = vi.fn();
  const withDoc = {
    ...detail,
    fileEdits: [{ path: 'docs/plan.md', adds: 1, dels: 0, edits: 1, isNew: true }],
  };
  await act(async () =>
    root.render(<RecapSection detail={withDoc} onEvidence={onEvidence} onOpenDesignDoc={onOpenDesignDoc} />),
  );
  const [cited, uncited] = mount.querySelectorAll<HTMLElement>('[tabindex="0"]');
  expect(cited.textContent).toBe('YOUPreserve history');
  await act(async () => cited.focus());
  expect(document.body.textContent).toContain('Audits require continuity');
  expect(document.body.textContent).toContain('Instead of Discarding branches');
  await act(async () => uncited.focus());
  expect(document.body.textContent).toContain('The quote was not found in the timeline.');
  await act(async () =>
    mount.querySelector<HTMLButtonElement>('[aria-label="Show turn 2 in the timeline"]')!.click(),
  );
  expect(onEvidence).toHaveBeenCalledWith(evidence);
  await act(async () => mount.querySelector<HTMLButtonElement>('button[title^="docs/plan.md"]')!.click());
  expect(onOpenDesignDoc).toHaveBeenCalledWith('docs/plan.md');
});

it('retains legacy bullets and searches structured reasons and alternatives', () => {
  const legacy = {
    ...detail,
    synthesis: { goals: [], outcome: '', nextStep: '', keyDecisions: ['Legacy decision'] },
  };
  expect(renderToStaticMarkup(<RecapSection detail={legacy} />)).toContain('Legacy decision');
  for (const search of ['continuity', 'discarding']) {
    expect(sessionMatchesSearchTerm(detail, search)).toBe(true);
  }
  const handoff = handoffBrief(detail);
  expect(handoff).toContain('Preserve history — Audits require continuity (instead of Discarding branches)');
  expect(handoff).toContain('Design doc: docs/plan.md');
  expect(handoff).not.toContain('Unverified choice');
});

it.each([
  ['uncached', null, 'your own CLI account and quota'],
  ['structured', detail.synthesis, 'Preserve history'],
  ['legacy', { goals: [], outcome: '', nextStep: '', keyDecisions: ['Legacy decision'] }, 'Legacy decision'],
] satisfies [string, typeof detail.synthesis, string][])(
  'opens settings without enabling synthesis for %s decisions',
  async (_, synthesis, expected) => {
    const open = vi.fn();
    await act(async () =>
      root.render(<RecapSection detail={{ ...detail, synthesis }} onOpenSynthesisSettings={open} />),
    );
    expect(mount.textContent).toContain(expected);
    const button = [...mount.querySelectorAll('button')].find(
      (candidate) => candidate.textContent === 'Choose synthesis settings',
    );
    expect(button).toBeDefined();
    await act(async () => button!.click());
    expect(open).toHaveBeenCalledOnce();
  },
);

it('clears a hiding filter, scrolls the full digest index, and shows the original quote', async () => {
  const scroll = vi.mocked(HTMLElement.prototype.scrollIntoView);
  await act(async () => root.render(<DigestSection detail={detail} />));
  await act(async () =>
    [...mount.querySelectorAll('button')]
      .find((button) => button.textContent?.startsWith('USER TURN'))!
      .click(),
  );
  expect(mount.querySelector('[data-entry="1"]')).toBeNull();
  await act(async () => root.render(<DigestSection detail={detail} evidence={evidence} />));
  const row = mount.querySelector('[data-entry="1"]')!;
  expect(row.textContent).toContain(`Cited quote: ${evidence.quote}`);
  expect(document.activeElement).toBe(row);
  expect(scroll).toHaveBeenCalledWith({ block: 'center' });
  await act(async () => root.render(<DigestSection key="new-revision" detail={detail} />));
  expect(mount.querySelector('[tabindex="-1"]')).toBeNull();
  expect(mount.textContent).not.toContain('Cited quote:');
});

it('keeps expanded state attached to a digest entry after an earlier entry is inserted', async () => {
  const longDescription = 'A detailed timeline entry that needs expansion. '.repeat(4);
  const initial = makeSession({
    sourceId: 'local',
    agent: 'pi',
    id: 'stable-digest-rows',
    digest: [
      {
        turn: 1,
        category: 'recap',
        sourceEntryId: 'target',
        description: longDescription,
      },
    ],
  });
  const refreshed = makeSession({
    ...initial,
    digest: [
      {
        ...initial.digest[0],
        category: 'todos',
        sourceEntryId: 'inserted',
      },
      ...initial.digest,
    ],
  });
  await act(async () => root.render(<DigestSection detail={initial} />));
  const expand = mount.querySelector('[data-entry="0"] button[aria-expanded="false"]')!;
  await act(async () => (expand as HTMLButtonElement).click());
  expect(mount.querySelector('[data-entry="0"] button[aria-expanded="true"]')).not.toBeNull();

  await act(async () => root.render(<DigestSection detail={refreshed} />));
  expect(mount.querySelector('[data-entry="1"] button[aria-expanded="true"]')).not.toBeNull();
  expect(mount.querySelector('[data-entry="0"] button[aria-expanded="false"]')).not.toBeNull();
});

it('finds the cited row when the stored index is shifted, and keeps a filter when the citation clears', async () => {
  const shifted = makeSession({
    sourceId: 'local',
    agent: 'claude',
    id: 'shifted',
    digest: [
      { category: 'user', turn: 1, description: 'Starting the work' },
      { category: 'todos', turn: 1, description: 'Plan the change' },
      { category: 'user', turn: 2, description: 'Please keep the complete source history' },
    ],
  });
  await act(async () => root.render(<DigestSection detail={shifted} evidence={evidence} />));
  expect(mount.querySelector('[tabindex="-1"]')?.getAttribute('data-entry')).toBe('2');
  expect(mount.textContent).not.toContain('Cited quote:');
  await act(async () =>
    [...mount.querySelectorAll('button')]
      .find((button) => button.textContent?.startsWith('USER TURN'))!
      .click(),
  );
  await act(async () => root.render(<DigestSection detail={shifted} evidence={null} />));
  expect(mount.querySelector('[data-entry="1"]')).toBeNull();
  expect(mount.querySelector('[tabindex="-1"]')).toBeNull();
});
