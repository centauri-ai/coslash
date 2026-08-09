import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { defaultDaGamaBoard, withComponent, type DaGamaBoard } from '@/plugins/canvas/dagama/board';
import { DaGamaBoardView, type DaGamaBoardViewProps } from '@/plugins/canvas/dagama/DaGamaCanvas';
import {
  FROZEN_DAGAMA_FAILED_RUN,
  FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN,
  FROZEN_DAGAMA_PROJECT,
  FROZEN_DAGAMA_PUBLISH_GATE_RUN,
  FROZEN_DAGAMA_REPAIR_GATE_RUN,
  FROZEN_DAGAMA_RUNNING_RUN,
} from '@/plugins/canvas/dagama/fixtures';
import type { DaGamaRun } from '@/plugins/canvas/dagama/types';

const NOOP = vi.fn();
const ASYNC = vi.fn(async () => {});

function render(overrides: Partial<DaGamaBoardViewProps> = {}): string {
  const props: DaGamaBoardViewProps = {
    board: defaultDaGamaBoard(),
    project: FROZEN_DAGAMA_PROJECT,
    boards: [],
    activeBoard: null,
    saveState: 'draft',
    boardError: '',
    run: null,
    runs: [],
    starting: false,
    runError: '',
    terminals: {},
    projectSuggestions: [],
    now: Date.parse('2026-08-09T05:20:00Z'),
    onBoardChange: NOOP,
    onChooseProject: ASYNC,
    onOpenBoard: NOOP,
    onDeleteBoard: NOOP,
    onNewBoard: NOOP,
    onSaveBoardAs: ASYNC,
    onKeepLocal: NOOP,
    onReloadFromServer: NOOP,
    onSelectRun: NOOP,
    onPreviewRun: vi.fn(async () => {
      throw new Error('not used');
    }),
    onStartRun: ASYNC,
    onReadArtifact: vi.fn(async () => ''),
    onReadPrompt: vi.fn(async () => ''),
    onReadPreflight: vi.fn(async () => {
      throw new Error('not used');
    }),
    onDecideGate: ASYNC,
    onRetrySeat: ASYNC,
    onCancelRun: ASYNC,
    onTakeover: ASYNC,
    onHandback: ASYNC,
    onTerminalInput: NOOP,
    onTerminalResize: NOOP,
    onReconnectTerminal: NOOP,
    onSendToSeat: ASYNC,
    ...overrides,
  };
  return renderToStaticMarkup(<DaGamaBoardView {...props} />);
}

describe('DaGama board', () => {
  it('renders the six fixed stages and the seat companions', () => {
    const html = render();
    for (const stage of ['intake', 'plan', 'build', 'verify', 'review', 'publish']) {
      expect(html).toContain(`canvas-node-${stage}`);
    }
    for (const companion of ['plan-prompt', 'plan-info', 'build-prompt', 'review-info']) {
      expect(html).toContain(`canvas-node-${companion}`);
    }
    // A deterministic stage has no seat, so it gets no companions.
    expect(html).not.toContain('canvas-node-verify-prompt');
  });

  it('describes an unconfigured stage by purpose and contracted outputs', () => {
    const html = render();
    expect(html).toContain('Snapshot the source and form the problem statement');
    expect(html).toContain('PLAN.md');
    expect(html).toContain('verification.json');
    expect(html).toContain('Produces');
  });

  it('summarizes each stage in its header', () => {
    const html = render();
    expect(html).toContain('claude · opus');
    // Review deliberately runs the other vendor family.
    expect(html).toContain('codex · gpt-5.6-terra');
    expect(html).toContain('no checks — skipped');
    expect(html).toContain('default branch');
  });

  it('blocks starting a run until a project is chosen', () => {
    const withoutProject = render({ project: null });
    expect(withoutProject).toContain('Choose a project before starting a run');
    expect(withoutProject).toContain('disabled=""');
    expect(withoutProject).toContain('Choose a project to save workflows');

    const ready = render({ saveState: 'saved', activeBoard: null });
    expect(ready).toContain('Set the title and source for this workflow');
  });

  it('blocks starting a run while the board is still saving', () => {
    expect(render({ saveState: 'saving' })).toContain('Wait for the board to save before running it');
  });

  it('offers both safe recoveries for a revision conflict and never overwrites silently', () => {
    const html = render({
      saveState: 'conflict',
      boardError: 'the board changed since it was loaded',
      activeBoard: {
        schemaVersion: 1,
        id: 'board-1',
        name: 'Logout button',
        revision: 3,
        createdAt: '2026-08-09T05:00:00Z',
        updatedAt: '2026-08-09T05:10:00Z',
      },
    });
    expect(html).toContain('Keep local');
    expect(html).toContain('Reload server');
    expect(html).toContain('the board changed since it was loaded');
    expect(html).toContain('Resolve the board conflict before running it');
  });

  it('shows a seat terminal only once its stage has an attempt', () => {
    const idle = render();
    expect(idle).toContain('The live CLI opens here when this stage runs');

    const running = render({ run: FROZEN_DAGAMA_RUNNING_RUN });
    expect(running).toContain('aria-label="Build seat terminal"');
    expect(running).toContain('attempt 1');
    expect(running).toContain('automated');
  });

  it('offers takeover on an automated attempt and handback once controlled', () => {
    const automated = render({ run: FROZEN_DAGAMA_RUNNING_RUN });
    expect(automated).toContain('>Take control<');
    expect(automated).not.toContain('>Return<');

    const controlled = render({ run: FROZEN_DAGAMA_HUMAN_CONTROLLED_RUN });
    expect(controlled).toContain('>Return<');
    expect(controlled).not.toContain('>Take control<');
  });

  it('offers retry only on a failed seat', () => {
    expect(render({ run: FROZEN_DAGAMA_FAILED_RUN })).toContain('>Retry<');
    expect(render({ run: FROZEN_DAGAMA_RUNNING_RUN })).not.toContain('>Retry<');
  });

  it('offers no seat control at all once the run is terminal', () => {
    const finished: DaGamaRun = { ...FROZEN_DAGAMA_FAILED_RUN, status: 'failed' };
    const html = render({ run: finished });
    expect(html).not.toContain('>Take control<');
    expect(html).not.toContain('>Retry<');
    expect(html).not.toContain('>Cancel<');
  });

  it('reports the seat failure reason from the taxonomy', () => {
    const html = render({ run: FROZEN_DAGAMA_FAILED_RUN });
    expect(html).toContain('missing_output');
    expect(html).toContain('The attempt exited without writing IMPLEMENTATION.md.');
  });

  it('renders the publish gate with its decision controls', () => {
    const html = render({ run: FROZEN_DAGAMA_PUBLISH_GATE_RUN });
    expect(html).toContain('Awaiting your approval');
    expect(html).toContain('Approve &amp; publish');
    expect(html).toContain('Approve without publish');
    expect(html).toContain('Reject');
  });

  it('withholds a stale gate decision and explains why', () => {
    const stale: DaGamaRun = {
      ...FROZEN_DAGAMA_PUBLISH_GATE_RUN,
      change: { ...FROZEN_DAGAMA_PUBLISH_GATE_RUN.change!, changeRevision: 5 },
    };
    const html = render({ run: stale });
    expect(html).toContain('Reload the run before deciding');
    expect(html).toContain('role="alert"');
    expect(html).toContain('disabled=""');
  });

  it('renders the repair gate on the component that opened it', () => {
    const html = render({ run: FROZEN_DAGAMA_REPAIR_GATE_RUN });
    expect(html).toContain('Repair rounds exhausted');
    expect(html).toContain('Retry Build');
    expect(html).toContain('Verify opened a human gate');
  });

  it('shows a published run instead of a gate once publication landed', () => {
    const published: DaGamaRun = {
      ...FROZEN_DAGAMA_PUBLISH_GATE_RUN,
      status: 'succeeded',
      gate: { ...FROZEN_DAGAMA_PUBLISH_GATE_RUN.gate!, decision: 'approved' },
      publication: {
        changeRevision: 2,
        commitSha: 'f'.repeat(40),
        branch: 'dagama/run-1',
        remote: 'origin',
        prUrl: 'https://github.com/example/demo/pull/7',
        prNumber: 7,
        action: 'created',
        idempotencyKey: 'key',
        publishedAt: '2026-08-09T05:30:00Z',
      },
    };
    const html = render({ run: published });
    expect(html).toContain('Published');
    expect(html).toContain('PR #7');
    expect(html).not.toContain('Awaiting your approval');
  });

  it('keeps the prompt card editable before a stage starts and swaps to compose after', () => {
    const idle = render({ board: withComponent(defaultDaGamaBoard(), 'plan', { prompt: 'be careful' }) });
    expect(idle).toContain('Plan prompt card');
    expect(idle).toContain('be careful');

    const running = render({ run: FROZEN_DAGAMA_RUNNING_RUN });
    expect(running).toContain('Build seat message');
    expect(running).toContain('Take control to send into this terminal…');
  });

  it('keeps configuration editable only while a stage has not started', () => {
    const editable = render();
    expect(editable).toContain('aria-label="Agent"');
    expect(editable).toContain('Add check');

    const running = render({ run: FROZEN_DAGAMA_RUNNING_RUN });
    // Build has started, so its seat is reported rather than offered for edit.
    expect(running).toContain('Claude Code · opus · high');
  });

  it('warns that the loosest Claude permission is not a sandbox', () => {
    const board: DaGamaBoard = withComponent(defaultDaGamaBoard(), 'build', {
      seat: { vendor: 'claude', model: 'opus', effort: 'high', permission: 'bypassPermissions' },
    });
    expect(render({ board })).toContain('Claude has no sandbox');
  });

  it('draws the forward pipeline, the repair return, and the seat cluster wires', () => {
    const html = render();
    expect(html).toContain('dagama-wire-flow');
    expect(html).toContain('dagama-wire-repair');
    expect(html).toContain('dagama-wire-cluster');
  });

  it('marks the stage the run is currently executing', () => {
    const html = render({ run: FROZEN_DAGAMA_RUNNING_RUN });
    expect(html).toContain('dagama-node-status-running');
    expect(html).toContain('dagama-node-status-succeeded');
    expect(html).toContain('dagama-wire-active');
  });

  it('surfaces a run error beside the save state', () => {
    const html = render({ runError: 'This run could not be opened.', saveState: 'saved' });
    expect(html).toContain('This run could not be opened.');
  });

  it('reports a seat terminal that could not be attached instead of pretending to connect', () => {
    const html = render({
      run: FROZEN_DAGAMA_RUNNING_RUN,
      terminals: { build: { snapshot: null, error: 'The seat terminal is unavailable.' } },
    });
    expect(html).toContain('The seat terminal is unavailable.');
  });

  it('renders live terminal output and its connection state', () => {
    const html = render({
      run: FROZEN_DAGAMA_RUNNING_RUN,
      terminals: { build: { snapshot: { status: 'open', output: 'building…', attempts: 0 }, error: '' } },
    });
    expect(html).toContain('building…');
    expect(html).toContain('· open');
  });

  it('offers a reconnect once the socket has dropped', () => {
    const html = render({
      run: FROZEN_DAGAMA_RUNNING_RUN,
      terminals: { build: { snapshot: { status: 'error', output: '', attempts: 5 }, error: '' } },
    });
    expect(html).toContain('Reconnect');
  });

  it('never renders an iframe or an external terminal URL', () => {
    const html = render({
      run: FROZEN_DAGAMA_RUNNING_RUN,
      terminals: { build: { snapshot: { status: 'open', output: 'x', attempts: 0 }, error: '' } },
    });
    expect(html).not.toContain('<iframe');
    expect(html).not.toMatch(/http:\/\/(127\.0\.0\.1|localhost):\d+/);
  });
});
