// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { renderToStaticMarkup } from 'react-dom/server';
import { expect, it, vi } from 'vitest';
import { decodeSession } from '../hooks/use-sessions';
import { handoffBrief } from '../lib/handoff';
import { ALL_REPOSITORIES, filterSessionLibrary } from '../lib/session-library';
import { DigestSection, RecapSection } from './SessionInspector';

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
const detail = decodeSession({
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

it('renders structured decisions, provenance, dimmed uncited claims and the design doc', () => {
  const html = renderToStaticMarkup(<RecapSection detail={detail} />);
  expect(html).toContain('Audits require continuity');
  expect(html).toContain('instead of Discarding branches');
  expect(html).toContain('You');
  expect(html).toContain('Turn 2');
  expect(html).toContain('The quote was not found in the timeline.');
  expect(html).toContain('Design doc:');
  expect(html).toContain('More decisions are in the design doc.');
  expect((html.match(/Turn 2/g) ?? []).length).toBe(1);
});

it('retains legacy bullets and searches structured reasons and alternatives', () => {
  const legacy = {
    ...detail,
    synthesis: { goals: [], outcome: '', nextStep: '', keyDecisions: ['Legacy decision'] },
  };
  expect(renderToStaticMarkup(<RecapSection detail={legacy} />)).toContain('Legacy decision');
  for (const search of ['continuity', 'discarding']) {
    expect(
      filterSessionLibrary([detail], {
        search,
        repository: ALL_REPOSITORIES,
        source: 'all',
        shareState: 'all',
      }),
    ).toEqual([detail]);
  }
  const handoff = handoffBrief(detail);
  expect(handoff).toContain('Preserve history — Audits require continuity (instead of Discarding branches)');
  expect(handoff).toContain('Design doc: docs/plan.md');
  expect(handoff).not.toContain('Unverified choice');
});

it('opens settings without saving or enabling synthesis', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const mount = document.createElement('div');
  const root = createRoot(mount);
  const open = vi.fn();
  try {
    await act(async () =>
      root.render(<RecapSection detail={{ ...detail, synthesis: null }} onOpenSynthesisSettings={open} />),
    );
    expect(mount.textContent).toContain('your own CLI account and quota');
    await act(async () => mount.querySelector('button')!.click());
    expect(open).toHaveBeenCalledOnce();
  } finally {
    await act(async () => root.unmount());
    vi.unstubAllGlobals();
  }
});

it.each(['structured', 'legacy'] as const)(
  'keeps settings access beside cached %s decisions',
  async (kind) => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    const mount = document.createElement('div');
    const root = createRoot(mount);
    const open = vi.fn();
    const cached =
      kind === 'structured'
        ? detail
        : decodeSession({
            sourceId: 'local',
            agent: 'pi',
            id: 'legacy-choices',
            synthesis: { goals: [], outcome: '', nextStep: '', keyDecisions: ['Legacy decision'] },
          });
    try {
      await act(async () => root.render(<RecapSection detail={cached} onOpenSynthesisSettings={open} />));
      expect(mount.textContent).toContain(kind === 'structured' ? 'Preserve history' : 'Legacy decision');
      const button = [...mount.querySelectorAll('button')].find(
        (candidate) => candidate.textContent === 'Choose synthesis settings',
      );
      expect(button).toBeDefined();
      await act(async () => button!.click());
      expect(open).toHaveBeenCalledOnce();
    } finally {
      await act(async () => root.unmount());
      vi.unstubAllGlobals();
    }
  },
);

it('clears a hiding filter, scrolls the full digest index, and shows the original quote', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const scroll = vi.fn();
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const previous = HTMLElement.prototype.scrollIntoView;
  HTMLElement.prototype.scrollIntoView = scroll;
  try {
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
  } finally {
    HTMLElement.prototype.scrollIntoView = previous;
    await act(async () => root.unmount());
    mount.remove();
    vi.unstubAllGlobals();
  }
});

it('keeps expanded state attached to a digest entry after an earlier entry is inserted', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const mount = document.createElement('div');
  const root = createRoot(mount);
  const longDescription = 'A detailed timeline entry that needs expansion. '.repeat(4);
  const initial = decodeSession({
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
  const refreshed = decodeSession({
    sourceId: 'local',
    agent: 'pi',
    id: 'stable-digest-rows',
    digest: [
      {
        turn: 1,
        category: 'todos',
        sourceEntryId: 'inserted',
        description: longDescription,
      },
      ...initial.digest,
    ],
  });
  try {
    await act(async () => root.render(<DigestSection detail={initial} />));
    const expand = mount.querySelector('[data-entry="0"] button[aria-expanded="false"]')!;
    await act(async () => (expand as HTMLButtonElement).click());
    expect(mount.querySelector('[data-entry="0"] button[aria-expanded="true"]')).not.toBeNull();

    await act(async () => root.render(<DigestSection detail={refreshed} />));
    expect(mount.querySelector('[data-entry="1"] button[aria-expanded="true"]')).not.toBeNull();
    expect(mount.querySelector('[data-entry="0"] button[aria-expanded="false"]')).not.toBeNull();
  } finally {
    await act(async () => root.unmount());
    vi.unstubAllGlobals();
  }
});

it('finds the cited row when the stored index is shifted, and keeps a filter when the citation clears', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  const shifted = decodeSession({
    sourceId: 'local',
    agent: 'claude',
    id: 'shifted',
    digest: [
      { category: 'user', turn: 1, description: 'Starting the work' },
      { category: 'todos', turn: 1, description: 'Plan the change' },
      { category: 'user', turn: 2, description: 'Please keep the complete source history' },
    ],
  });
  try {
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
  } finally {
    await act(async () => root.unmount());
    mount.remove();
    vi.unstubAllGlobals();
  }
});
