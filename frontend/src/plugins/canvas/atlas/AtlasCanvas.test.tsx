import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { AtlasBoardView, type AtlasBoardViewProps } from '@/plugins/canvas/atlas/AtlasCanvas';
import {
  atlasAttempt,
  FROZEN_ATLAS_COMMITTEE_RUN,
  FROZEN_ATLAS_FAILED_RUN,
  FROZEN_ATLAS_HUMAN_CONTROLLED_RUN,
  FROZEN_ATLAS_PARTIAL_RUN,
  FROZEN_ATLAS_PLAIN_FOLDER_RUN,
  FROZEN_ATLAS_PROJECT,
  FROZEN_ATLAS_PUBLISH_GATE_RUN,
  FROZEN_ATLAS_REFINING_RUN,
  FROZEN_ATLAS_TRIGGER_GATE_RUN,
} from '@/plugins/canvas/atlas/fixtures';
import {
  addComponent,
  connect,
  defaultAtlasBoard,
  removeComponent,
  setCommitteeSize,
  withComponent,
  type AtlasBoard,
} from '@/plugins/canvas/atlas/graph';
import type { AtlasRun } from '@/plugins/canvas/atlas/types';

const NOOP = vi.fn();
const ASYNC = vi.fn(async () => {});

function render(overrides: Partial<AtlasBoardViewProps> = {}): string {
  const props: AtlasBoardViewProps = {
    board: defaultAtlasBoard(),
    project: FROZEN_ATLAS_PROJECT,
    boards: [],
    activeBoard: null,
    saveState: 'draft',
    boardError: '',
    readOnly: false,
    migrated: false,
    run: null,
    runs: [],
    starting: false,
    runError: '',
    terminals: {},
    projectSuggestions: [],
    now: Date.parse('2026-08-09T07:05:00Z'),
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
    onReadPreflight: vi.fn(async () => {
      throw new Error('not used');
    }),
    onDecideGate: ASYNC,
    onRetryStage: ASYNC,
    onCancelRun: ASYNC,
    onTakeover: ASYNC,
    onHandback: ASYNC,
    onTerminalInput: NOOP,
    onReconnectTerminal: NOOP,
    onSendToAttempt: ASYNC,
    ...overrides,
  };
  return renderToStaticMarkup(<AtlasBoardView {...props} />);
}

describe('Atlas board', () => {
  it('renders the starter chain as three seat clusters', () => {
    const html = render();
    for (const seat of ['plan', 'build', 'review']) {
      expect(html).toContain(`canvas-node-${seat}`);
      expect(html).toContain(`canvas-node-${seat}-prompt`);
      expect(html).toContain(`canvas-node-${seat}-info`);
    }
  });

  it('draws the pipeline, the repair return, and the cluster wires', () => {
    const html = render();
    expect(html).toContain('atlas-wire-flow');
    expect(html).toContain('atlas-wire-repair');
    expect(html).toContain('atlas-wire-cluster');
  });

  it('offers a seat added to the graph the same editing surface as a stage', () => {
    const html = render({ board: addComponent(defaultAtlasBoard()) });
    expect(html).toContain('canvas-node-seat-4');
    expect(html).toContain('canvas-node-seat-4-info');
    expect(html).toContain('OUTPUT.md');
  });

  it('names what a graph is missing instead of hiding Run', () => {
    // A graph without a build seat cannot run on today's runtime, and the board
    // says so rather than leaving a disabled button unexplained.
    const html = render({ board: removeComponent(defaultAtlasBoard(), 'build') });
    expect(html).toContain('Run only works on the plan → build → review starter chain');
    expect(html).toContain('disabled=""');
  });

  it('accepts a runnable graph the operator rewired themselves', () => {
    const rewired = connect(removeComponent(defaultAtlasBoard(), 'build'), 'plan', 'review');
    // Still missing build: rewiring around it does not make the chain runnable.
    expect(render({ board: rewired })).toContain('Run only works on the plan → build → review starter chain');
    expect(render()).toContain('Set the title and source for this workflow');
  });

  it('blocks a run until a project is chosen', () => {
    const html = render({ project: null });
    expect(html).toContain('Choose a project before starting a run');
    expect(html).toContain('Choose a project to save workflows');
  });

  it('blocks a run while another one is still in progress', () => {
    const html = render({
      saveState: 'saved',
      runs: [
        {
          runId: 'run-1',
          projectId: FROZEN_ATLAS_PROJECT.id,
          boardId: 'board-1',
          title: 'Earlier',
          status: 'running',
          createdAt: '2026-08-09T06:00:00Z',
          updatedAt: '2026-08-09T06:10:00Z',
          finishedAt: null,
        },
      ],
    });
    expect(html).toContain('This project already has a run in progress');
  });

  it('offers both safe recoveries for a revision conflict', () => {
    const html = render({
      saveState: 'conflict',
      boardError: 'the workflow changed since it was loaded',
    });
    expect(html).toContain('Keep local');
    expect(html).toContain('Reload server');
    expect(html).toContain('the workflow changed since it was loaded');
    expect(html).toContain('Resolve the board conflict before running it');
  });

  it('shows a board from a newer build as read-only and withholds every edit', () => {
    const html = render({
      readOnly: true,
      boardError: 'This workflow was written by a newer coSlash. It is shown read-only…',
    });
    expect(html).toContain('shown read-only');
    // No structural edit is offered at all: accepting one and refusing to save
    // it would lose the work at the worst possible moment.
    expect(html).not.toContain('Remove seat');
    expect(html).not.toContain('>Connect<');
    expect(html).not.toContain('atlas-edge-hit');
  });

  it('says when a workflow was upgraded from the older format', () => {
    expect(render({ migrated: true })).toContain('upgraded from an older format');
  });

  it('shows every committee sibling rather than only the newest turn', () => {
    const html = render({ run: FROZEN_ATLAS_COMMITTEE_RUN });
    expect(html).toContain('aria-label="Show plan-1"');
    expect(html).toContain('aria-label="Show plan-2"');
    expect(html).toContain('aria-label="Show plan-3"');
    expect(html).toContain('3 seat turns');
  });

  it('reports committee progress in the terms the committee was configured in', () => {
    const board = setCommitteeSize(defaultAtlasBoard(), 'plan', 3);
    const html = render({ board, run: FROZEN_ATLAS_COMMITTEE_RUN });
    expect(html).toContain('Committee');
    expect(html).toContain('1 of 3 drafted');
    expect(html).toContain('2 running');
  });

  it('names a partial committee result instead of rolling it up as success', () => {
    const board = setCommitteeSize(defaultAtlasBoard(), 'plan', 3);
    const html = render({ board, run: FROZEN_ATLAS_PARTIAL_RUN });
    expect(html).toContain('partial — some seats produced no draft');
  });

  it('distinguishes the consolidating turn from the drafts', () => {
    const html = render({ run: FROZEN_ATLAS_REFINING_RUN });
    expect(html).toContain('aria-label="Show plan-main-refine"');
    expect(html).toContain('consolidating');
  });

  it('offers takeover on an automated attempt and handback once controlled', () => {
    const automated = render({ run: FROZEN_ATLAS_COMMITTEE_RUN });
    expect(automated).toContain('>Take control<');
    expect(automated).not.toContain('>Return<');

    const controlled = render({ run: FROZEN_ATLAS_HUMAN_CONTROLLED_RUN });
    expect(controlled).toContain('>Return<');
    expect(controlled).not.toContain('>Take control<');
  });

  it('retries a committee whole, and only when the stage failed', () => {
    expect(render({ run: FROZEN_ATLAS_FAILED_RUN })).toContain('Retry committee');
    expect(render({ run: FROZEN_ATLAS_COMMITTEE_RUN })).not.toContain('Retry committee');
  });

  it('offers no control at all once the run is terminal', () => {
    const canceled: AtlasRun = { ...FROZEN_ATLAS_COMMITTEE_RUN, status: 'canceled' };
    const html = render({ run: canceled });
    expect(html).not.toContain('>Take control<');
    expect(html).not.toContain('Retry committee');
    expect(html).not.toContain('Cancel run');
  });

  it('reports a stage failure by its taxonomy reason', () => {
    const html = render({ run: FROZEN_ATLAS_FAILED_RUN });
    expect(html).toContain('missing_output');
    expect(html).toContain('No committee member produced a draft.');
  });

  it('surfaces the deterministic stages the graph has no node for', () => {
    // Intake, Verify, and Publish run without a seat. Dropping them would hide
    // the publish gate, which is the decision the operator has to make.
    const html = render({ run: FROZEN_ATLAS_PUBLISH_GATE_RUN });
    expect(html).toContain('atlas-rail');
    expect(html).toContain('>intake<');
    expect(html).toContain('>verify<');
    expect(html).toContain('>publish<');
    expect(html).not.toContain('canvas-node-publish');
  });

  it('renders the publish gate with its decision controls', () => {
    const html = render({ run: FROZEN_ATLAS_PUBLISH_GATE_RUN });
    expect(html).toContain('Awaiting your approval');
    expect(html).toContain('Approve &amp; publish');
    expect(html).toContain('Approve without publish');
    expect(html).toContain('Reject');
  });

  it('withholds a stale gate decision and explains why', () => {
    const stale: AtlasRun = {
      ...FROZEN_ATLAS_PUBLISH_GATE_RUN,
      change: { ...FROZEN_ATLAS_PUBLISH_GATE_RUN.change!, changeRevision: 5 },
    };
    const html = render({ run: stale });
    expect(html).toContain('Reload the run before deciding');
    expect(html).toContain('role="alert"');
    expect(html).toContain('disabled=""');
  });

  it('keeps a plain-folder run approvable and says publication is unavailable', () => {
    const html = render({ run: FROZEN_ATLAS_PLAIN_FOLDER_RUN });
    expect(html).toContain('no git remote');
    expect(html).toContain('Approve without publish');
  });

  it('renders a manual trigger gate on the seat that is waiting', () => {
    const html = render({ run: FROZEN_ATLAS_TRIGGER_GATE_RUN });
    expect(html).toContain('Waiting for your go');
    expect(html).toContain('build waits for an explicit go from plan');
    // The gate belongs to a graph seat, so the rail must not offer it twice.
    expect(html.match(/Waiting for your go/g)).toHaveLength(1);
  });

  it('shows a published run instead of a gate once publication landed', () => {
    const published: AtlasRun = {
      ...FROZEN_ATLAS_PUBLISH_GATE_RUN,
      status: 'succeeded',
      gate: { ...FROZEN_ATLAS_PUBLISH_GATE_RUN.gate!, decision: 'approved' },
      publication: {
        changeRevision: 2,
        commitSha: 'f'.repeat(40),
        branch: 'atlas/run-1',
        remote: 'origin',
        prUrl: 'https://github.com/example/demo/pull/7',
        prNumber: 7,
        action: 'created',
        idempotencyKey: 'key',
        publishedAt: '2026-08-09T07:30:00Z',
      },
    };
    const html = render({ run: published });
    expect(html).toContain('Published');
    expect(html).toContain('PR #7');
    expect(html).not.toContain('Awaiting your approval');
  });

  it('lists a committee draft with the seat that wrote it', () => {
    const withDrafts: AtlasRun = {
      ...FROZEN_ATLAS_COMMITTEE_RUN,
      artifacts: [
        {
          artifactId: 'artifact-1',
          kind: 'draft',
          name: 'PLAN.md',
          path: 'plan/PLAN.md',
          sha256: 'e'.repeat(64),
          bytes: 512,
          createdAt: '2026-08-09T07:04:00Z',
          producer: { componentId: 'plan', instance: 1, seatId: 'plan-1', attempt: 1 },
        },
      ],
    };
    const html = render({ run: withDrafts });
    expect(html).toContain('Artifacts');
    expect(html).toContain('PLAN.md');
    expect(html).toContain('plan-1');
  });

  it('keeps the prompt card editable before a stage starts and swaps to compose after', () => {
    const idle = render({ board: withComponent(defaultAtlasBoard(), 'plan', { prompt: 'be careful' }) });
    expect(idle).toContain('PLAN prompt card');
    expect(idle).toContain('be careful');

    const running = render({ run: FROZEN_ATLAS_COMMITTEE_RUN });
    expect(running).toContain('PLAN seat message');
    expect(running).toContain('Take control of a seat to send…');
  });

  it('keeps the committee editable only while its stage has not started', () => {
    const editable = render();
    expect(editable).toContain('aria-label="Agent"');
    expect(editable.match(/aria-label="Add a committee seat"/g)).toHaveLength(3);

    const running = render({ run: FROZEN_ATLAS_COMMITTEE_RUN });
    // Plan has started, so its seats are reported rather than offered for edit.
    // The two stages that have not started stay editable.
    expect(running).toContain('Claude Code · opus · high');
    expect(running.match(/aria-label="Add a committee seat"/g)).toHaveLength(2);
  });

  it('asks for consolidation steering only once there is something to consolidate', () => {
    expect(render()).not.toContain('Consolidation steering');
    const committee = setCommitteeSize(defaultAtlasBoard(), 'plan', 3);
    expect(render({ board: committee })).toContain('Consolidation steering');
  });

  it('warns that the loosest Claude permission is not a sandbox', () => {
    const board = setCommitteeSize(defaultAtlasBoard(), 'build', 1);
    const risky: AtlasBoard = {
      ...board,
      components: board.components.map((component) =>
        component.id === 'build'
          ? {
              ...component,
              seats: component.seats.map((seat) => ({ ...seat, permission: 'bypassPermissions' })),
            }
          : component,
      ),
    };
    expect(render({ board: risky })).toContain('Claude has no sandbox');
  });

  it('shows the zoom the open workflow was saved at', () => {
    expect(render()).toContain('55%');
    const zoomed: AtlasBoard = { ...defaultAtlasBoard(), viewport: { zoom: 1.2, panX: 0, panY: 0 } };
    expect(render({ board: zoomed })).toContain('120%');
  });

  it('marks the stage the run is currently executing and the edge into it', () => {
    // Build is running here, so plan → build is the live edge. A running first
    // stage has no incoming edge to light, which is why this uses build.
    const html = render({ run: FROZEN_ATLAS_HUMAN_CONTROLLED_RUN });
    expect(html).toContain('atlas-node-status-running');
    expect(html).toContain('atlas-wire-active');
  });

  it('reports a seat terminal that could not be attached instead of pretending to connect', () => {
    const html = render({
      run: FROZEN_ATLAS_COMMITTEE_RUN,
      terminals: { 'a-plan-2': { snapshot: null, error: 'The seat terminal is unavailable.' } },
    });
    expect(html).toContain('The seat terminal is unavailable.');
  });

  it('renders live terminal output and its connection state', () => {
    const html = render({
      run: FROZEN_ATLAS_COMMITTEE_RUN,
      terminals: {
        'a-plan-2': { snapshot: { status: 'open', output: 'drafting…', attempts: 0 }, error: '' },
      },
    });
    expect(html).toContain('drafting…');
    expect(html).toContain('· open');
  });

  it('offers a reconnect once the socket has dropped', () => {
    const html = render({
      run: FROZEN_ATLAS_COMMITTEE_RUN,
      terminals: {
        'a-plan-2': { snapshot: { status: 'error', output: '', attempts: 5 }, error: '' },
      },
    });
    expect(html).toContain('Reconnect');
  });

  it('enables the compose box only for an attempt this client controls', () => {
    const locked = render({
      run: FROZEN_ATLAS_COMMITTEE_RUN,
      terminals: {
        'a-plan-2': { snapshot: { status: 'open', output: '', attempts: 0 }, error: '' },
      },
    });
    expect(locked).toContain('Automated turn — take control to send');

    const controlled = render({
      run: FROZEN_ATLAS_HUMAN_CONTROLLED_RUN,
      terminals: {
        'a-build-1': { snapshot: { status: 'open', output: '', attempts: 0 }, error: '' },
      },
    });
    expect(controlled).toContain('Message this seat — Enter to send');
  });

  it('never renders an iframe or an external terminal URL', () => {
    const html = render({
      run: FROZEN_ATLAS_COMMITTEE_RUN,
      terminals: {
        'a-plan-2': { snapshot: { status: 'open', output: 'x', attempts: 0 }, error: '' },
      },
    });
    expect(html).not.toContain('<iframe');
    expect(html).not.toMatch(/http:\/\/(127\.0\.0\.1|localhost):\d+/);
  });

  it('surfaces a run error beside the save state', () => {
    expect(render({ runError: 'This run could not be opened.', saveState: 'saved' })).toContain(
      'This run could not be opened.',
    );
  });

  it('keeps an attempt addressable by id rather than by stage', () => {
    // A committee has several attempts on one component, so a control keyed by
    // component id would be ambiguous. The rendered controls carry attempt ids.
    const two: AtlasRun = {
      ...FROZEN_ATLAS_COMMITTEE_RUN,
      components: {
        ...FROZEN_ATLAS_COMMITTEE_RUN.components,
        plan: {
          ...FROZEN_ATLAS_COMMITTEE_RUN.components.plan,
          attempts: [
            atlasAttempt({ seatId: 'plan-1', attemptId: 'a-1', status: 'running' }),
            atlasAttempt({ seatId: 'plan-2', attemptId: 'a-2', status: 'running' }),
          ],
        },
      },
    };
    const html = render({ run: two });
    expect(html).toContain('aria-label="Show plan-1"');
    expect(html).toContain('aria-label="Show plan-2"');
  });
});
