/* oxlint-disable react/only-export-components -- The board ships its presentational view and its connected container together. */
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ComponentType,
  type KeyboardEvent as ReactKeyboardEvent,
} from 'react';
import {
  FileTextIcon,
  FolderOpenIcon,
  InfoIcon,
  MessageSquareIcon,
  PlayIcon,
  PlusIcon,
  TerminalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  WaypointsIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { Session } from '@/pages/coslash/lib/session';
import {
  cancelAtlasRun,
  decideAtlasGate,
  handbackAtlasAttempt,
  previewAtlasRun,
  readAtlasArtifact,
  readAtlasPublishPreflight,
  reconnectAtlasTerminal,
  retryAtlasStage,
  takeoverAtlasAttempt,
  type AtlasBoardSummary,
  type AtlasFetch,
} from '@/plugins/canvas/atlas/api';
import {
  ArtifactDialog,
  BoardMenu,
  ProjectDialog,
  RunChip,
  RunDialog,
  SaveBoardDialog,
} from '@/plugins/canvas/atlas/dialogs';
import {
  addComponent,
  applyLayoutUpdates,
  ATLAS_NODE_MIN_HEIGHT,
  ATLAS_NODE_MIN_WIDTH,
  ATLAS_WORLD,
  ATLAS_ZOOM_BOUNDS,
  boardContentExtent,
  componentById,
  connect,
  disconnect,
  layoutForNode,
  parseAtlasNodeId,
  removeComponent,
  runnableBlockedReason,
  seatInfoNodeId,
  seatPromptNodeId,
  setCommitteeSize,
  setWorkerSeat,
  withComponent,
  type AtlasBoard,
  type AtlasComponent,
} from '@/plugins/canvas/atlas/graph';
import {
  CommitteeEditor,
  CommitteeProgressStrip,
  CommitteeTerminalPane,
  PublishGatePane,
  RepairGatePane,
  RunRail,
  RunStateStrip,
  SeatPromptPane,
  TriggerGatePane,
} from '@/plugins/canvas/atlas/panes';
import { createAtlasRunSession } from '@/plugins/canvas/atlas/run-session';
import {
  attemptControls,
  committeeProgress,
  componentOf,
  gateView,
  isTerminalRun,
  offBoardGate,
  offBoardStages,
  publicationUnavailableReason,
  runBlockedReason,
  showsConfiguration,
  showsSeatTerminal,
  stageControls,
} from '@/plugins/canvas/atlas/runs';
import { createAtlasBoardSession, type AtlasSaveState } from '@/plugins/canvas/atlas/session';
import {
  bracketedPaste,
  createTerminalConnection,
  type TerminalConnection,
  type TerminalConnectionSnapshot,
} from '@/plugins/canvas/atlas/terminal';
import type {
  AtlasProject,
  AtlasPublishPreflight,
  AtlasRun,
  AtlasRunPreview,
  AtlasRunSummary,
  AtlasSourceInput,
} from '@/plugins/canvas/atlas/types';
import {
  boardCommandFor,
  CanvasNode,
  CanvasStage,
  CanvasWires,
  CanvasWorldLayer,
  clampZoom,
  fitZoom,
  isTextEntryTarget,
  nodeCenter,
  triggerWirePath,
  useCanvasNodeInteraction,
  wirePath,
  ZoomControls,
} from '@/plugins/canvas/shared';
import '@/plugins/canvas/atlas/atlas.css';

/** One attempt's terminal, as this client currently sees it. */
export type AtlasTerminalView = {
  snapshot: TerminalConnectionSnapshot | null;
  error: string;
};

export type AtlasBoardViewProps = {
  board: AtlasBoard;
  project: AtlasProject | null;
  boards: readonly AtlasBoardSummary[];
  activeBoard: AtlasBoardSummary | null;
  saveState: AtlasSaveState;
  boardError: string;
  /** True when the stored document declares a schema this build cannot write. */
  readOnly: boolean;
  migrated: boolean;
  run: AtlasRun | null;
  runs: readonly AtlasRunSummary[];
  starting: boolean;
  runError: string;
  /** Keyed by attempt id: a committee has several terminals open at once. */
  terminals: Record<string, AtlasTerminalView>;
  projectSuggestions: readonly string[];
  now: number;
  onBoardChange: (board: AtlasBoard) => void;
  onChooseProject: (projectPath: string) => Promise<void>;
  onOpenBoard: (summary: AtlasBoardSummary) => void;
  onDeleteBoard: (summary: AtlasBoardSummary) => void;
  onNewBoard: () => void;
  onSaveBoardAs: (name: string) => Promise<void>;
  onKeepLocal: () => void;
  onReloadFromServer: () => void;
  onSelectRun: (runId: string | null) => void;
  onPreviewRun: (input: { boardId: string } | { board: AtlasBoard }) => Promise<AtlasRunPreview>;
  onStartRun: (input: { source: AtlasSourceInput; boardName?: string }) => Promise<void>;
  onReadArtifact: (name: string) => Promise<string>;
  onReadPreflight: () => Promise<AtlasPublishPreflight>;
  onDecideGate: (decision: 'approved' | 'rejected', options?: { publish?: boolean }) => Promise<void>;
  onRetryStage: (componentId: string) => Promise<void>;
  onCancelRun: () => Promise<void>;
  onTakeover: (attemptId: string) => Promise<void>;
  onHandback: (attemptId: string) => Promise<void>;
  onTerminalInput: (attemptId: string, data: string) => void;
  onReconnectTerminal: (attemptId: string) => void;
  onSendToAttempt: (attemptId: string, text: string) => Promise<void>;
};

/**
 * The Atlas board.
 *
 * Presentational: every effect that reaches the collector arrives as a prop, so
 * the graph, its committees, and its gating can be rendered and asserted
 * without a server. `AtlasCanvas` below is the connected container.
 *
 * The editable graph is what separates Atlas from DaGama's fixed chain — seats
 * are added, wired, and sized here, and a graph that cannot run says why rather
 * than failing at start.
 */
export function AtlasBoardView(props: AtlasBoardViewProps) {
  const { board, project, run, saveState, readOnly } = props;
  const [selected, setSelected] = useState<string | null>(null);
  const [linkFrom, setLinkFrom] = useState<string | null>(null);
  const [selectedAttempts, setSelectedAttempts] = useState<Record<string, string>>({});
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [projectOpen, setProjectOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const [artifact, setArtifact] = useState<{ runId: string; name: string; producer: string } | null>(null);
  const stageRef = useRef<HTMLDivElement>(null);

  // Zoom is read from the board, so opening a different workflow shows the zoom
  // that workflow was saved at instead of silently keeping the previous one.
  const zoom = board.viewport.zoom;
  const graphReason = runnableBlockedReason(board);
  const conflict = saveState === 'conflict';

  const interaction = useCanvasNodeInteraction<string>({
    zoom,
    disabled: readOnly,
    world: ATLAS_WORLD,
    minWidth: ATLAS_NODE_MIN_WIDTH,
    minHeight: ATLAS_NODE_MIN_HEIGHT,
    getLayout: (id) => layoutForNode(board, id),
    updateLayouts: (updates) => props.onBoardChange(applyLayoutUpdates(board, updates)),
    onSelect: setSelected,
    // Dragging a seat terminal carries its prompt and info companions.
    getCompanions: (id) =>
      parseAtlasNodeId(id).role === 'terminal' ? [seatPromptNodeId(id), seatInfoNodeId(id)] : [],
  });

  const setZoomPersisted = (next: number) => {
    const clamped = clampZoom(ATLAS_ZOOM_BOUNDS, next);
    if (clamped === zoom) return;
    props.onBoardChange({ ...board, viewport: { ...board.viewport, zoom: clamped } });
  };

  const fitGraph = () => {
    const rect = stageRef.current?.getBoundingClientRect();
    if (rect === undefined) return;
    // Fit what the graph actually draws, not the whole world, so a board whose
    // seats sit in one corner does not zoom out to nothing.
    setZoomPersisted(
      fitZoom(ATLAS_ZOOM_BOUNDS, boardContentExtent(board), {
        width: rect.width - 64,
        height: rect.height - 96,
      }),
    );
  };

  const onBoardKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (isTextEntryTarget(event.target as HTMLElement)) return;
    const command = boardCommandFor(event);
    if (command === null) return;
    event.preventDefault();
    if (command === 'exit-focus') {
      setSelected(null);
      // Escape also abandons a half-drawn connection, which is otherwise a mode
      // the operator has no way to leave.
      setLinkFrom(null);
      return;
    }
    if (command === 'zoom-reset') fitGraph();
    else setZoomPersisted(zoom + (command === 'zoom-in' ? ATLAS_ZOOM_BOUNDS.step : -ATLAS_ZOOM_BOUNDS.step));
  };

  const updateComponent = (id: string, patch: Partial<AtlasComponent>) =>
    props.onBoardChange(withComponent(board, id, patch));

  const blockedReason = runBlockedReason({
    hasProject: project !== null,
    graphReason,
    saveState,
    runs: props.runs,
  });

  const statusText = conflict
    ? props.boardError || 'This workflow changed on disk while you were editing it.'
    : props.runError !== ''
      ? props.runError
      : saveState === 'saving'
        ? 'Saving…'
        : saveState === 'loading'
          ? 'Opening…'
          : saveState === 'error'
            ? props.boardError || 'Workflow storage failed'
            : props.activeBoard && project
              ? `${props.activeBoard.name} · saved to ${project.name}`
              : project
                ? 'Unsaved workflow — save it to this project'
                : 'Choose a project to save workflows';

  /** Connecting is a two-click gesture: pick a source, then pick a target. */
  const onLinkClick = (componentId: string) => {
    if (linkFrom === null) {
      setLinkFrom(componentId);
      return;
    }
    if (linkFrom !== componentId) props.onBoardChange(connect(board, linkFrom, componentId));
    setLinkFrom(null);
  };

  const openArtifact = (name: string, producer: string) => {
    if (run !== null) setArtifact({ runId: run.runId, name, producer });
  };

  // Intake, Verify, and Publish run without a seat, so the graph has no node for
  // them. The rail is their home — and the publish gate's.
  const boardComponentIds = board.components.map((component) => component.id);
  const railStages = offBoardStages(run, boardComponentIds);
  const railGate = offBoardGate(run, boardComponentIds);
  const showRail =
    run !== null &&
    (railStages.length > 0 || railGate.open || run.publication !== null || run.failure !== null);

  /** The one attempt this client may write into, if it holds any. */
  const controllableAttemptId = (): string | null => {
    for (const [attemptId, view] of Object.entries(props.terminals)) {
      if (view.snapshot?.status !== 'open') continue;
      if (attemptControls(run, attemptId, { connected: true }).canSend) return attemptId;
    }
    return null;
  };
  const sendTarget = controllableAttemptId();

  const nodeChrome = (id: string, title: string, icon: ComponentType<{ className?: string }>) => ({
    id,
    title,
    icon,
    layout: layoutForNode(board, id),
    selected: selected === id,
    focused: false,
    focusActive: false,
    onSelect: setSelected,
    // The scrolling shared stage has no pan, so focus scrolls the node into view.
    onFocus: (node: string) => {
      setSelected(node);
      document.querySelector(`.canvas-node-${node}`)?.scrollIntoView({ block: 'center', inline: 'center' });
    },
    onToggleCollapse: (node: string) =>
      props.onBoardChange(
        applyLayoutUpdates(board, [[node, (layout) => ({ ...layout, collapsed: !layout.collapsed })]]),
      ),
    onToggleLock: (node: string) =>
      props.onBoardChange(
        applyLayoutUpdates(board, [[node, (layout) => ({ ...layout, locked: !layout.locked })]]),
      ),
    onDragStart: interaction.onDragStart,
    onResizeStart: interaction.onResizeStart,
  });

  return (
    <div className="atlas-board" onKeyDown={onBoardKeyDown}>
      <div ref={stageRef} className="relative min-h-0 flex-1">
        <CanvasStage
          toolbar={
            <>
              <div className="flex items-center gap-2">
                <div className="atlas-toolbar-group">
                  <Button
                    variant="ghost"
                    size="xs"
                    onClick={() => setProjectOpen(true)}
                    title={project?.path ?? 'Choose the project that owns these workflows'}
                  >
                    <FolderOpenIcon />
                    {project?.name ?? 'Choose project'}
                  </Button>
                  <BoardMenu
                    boards={props.boards}
                    activeBoardId={props.activeBoard?.id ?? null}
                    projectReady={project !== null}
                    onOpen={props.onOpenBoard}
                    onDelete={props.onDeleteBoard}
                    onNew={() => {
                      props.onNewBoard();
                      setSelected(null);
                      setLinkFrom(null);
                    }}
                    onSaveAs={() => setSaveOpen(true)}
                  />
                </div>
                <div className="atlas-toolbar-group">
                  <Button
                    variant="ghost"
                    size="xs"
                    disabled={readOnly}
                    title={readOnly ? 'This workflow is read-only' : 'Add a seat to this graph'}
                    onClick={() => props.onBoardChange(addComponent(board))}
                  >
                    <PlusIcon />
                    Add seat
                  </Button>
                  <Button
                    variant="ghost"
                    size="xs"
                    className="text-brand font-semibold"
                    disabled={blockedReason !== null}
                    title={blockedReason ?? 'Set the title and source for this workflow'}
                    onClick={() => setRunOpen(true)}
                  >
                    <PlayIcon />
                    Start run
                  </Button>
                  <RunChip activeRun={run} runs={props.runs} onSelect={props.onSelectRun} />
                </div>
              </div>
              <div className="atlas-status" role="status">
                <span
                  className={cn('size-1.5 rounded-full', {
                    'bg-success': saveState === 'saved' && props.runError === '' && !readOnly,
                    'bg-warning': saveState === 'saving' || saveState === 'loading' || readOnly,
                    'animate-pulse': saveState === 'saving' || saveState === 'loading',
                    'bg-destructive': saveState === 'error' || conflict || props.runError !== '',
                    'bg-muted-foreground': saveState === 'draft' && props.runError === '',
                  })}
                />
                <span>{statusText}</span>
                {conflict && (
                  <span className="flex items-center gap-1">
                    <Button variant="ghost" size="xs" onClick={props.onKeepLocal}>
                      Keep local
                    </Button>
                    <Button variant="ghost" size="xs" onClick={props.onReloadFromServer}>
                      Reload server
                    </Button>
                  </span>
                )}
              </div>
            </>
          }
        >
          {/* A graph that cannot run says what it is waiting for, rather than
              leaving a disabled button with no explanation. */}
          {(readOnly || props.migrated || graphReason !== '') && (
            <div className="atlas-banners">
              {readOnly && (
                <div className="atlas-banner atlas-banner-readonly" role="status">
                  <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
                  <span>{props.boardError}</span>
                </div>
              )}
              {props.migrated && (
                <div className="atlas-banner" role="status">
                  <InfoIcon className="mt-0.5 size-3 shrink-0" />
                  <span>
                    This workflow was upgraded from an older format. Its seats and connections are now
                    explicit, and editable.
                  </span>
                </div>
              )}
              {graphReason !== '' && (
                <div className="atlas-banner atlas-graph-blocked" role="status">
                  <WaypointsIcon className="mt-0.5 size-3 shrink-0" />
                  <span>{graphReason}</span>
                </div>
              )}
            </div>
          )}

          <CanvasWorldLayer world={ATLAS_WORLD} zoom={zoom} hasFocus={false}>
            <CanvasWires world={ATLAS_WORLD}>
              <g className="atlas-wires">
                {board.edges.map((edge) => {
                  const from = componentById(board, edge.from);
                  const to = componentById(board, edge.to);
                  if (from === null || to === null) return null;
                  const path =
                    edge.kind === 'feedback'
                      ? wirePath(nodeCenter(from.box), nodeCenter(to.box))
                      : triggerWirePath(from.box, to.box);
                  return (
                    <g key={edge.id}>
                      <path
                        className={cn(edge.kind === 'feedback' ? 'atlas-wire-repair' : 'atlas-wire-flow', {
                          'atlas-wire-active': componentOf(run, edge.to)?.status === 'running',
                        })}
                        d={path}
                      />
                      {/* A wire is removable only while the graph is editable; a
                          thick invisible hit path makes it clickable at any zoom. */}
                      {!readOnly && (
                        <path
                          className="atlas-edge-hit"
                          d={path}
                          role="button"
                          aria-label={`Disconnect ${edge.from} from ${edge.to}`}
                          onClick={() => props.onBoardChange(disconnect(board, edge.id))}
                        />
                      )}
                    </g>
                  );
                })}
                {board.components.map((component) => {
                  const terminal = nodeCenter(component.box);
                  return (
                    <g key={`cluster-${component.id}`}>
                      <path
                        className="atlas-wire-cluster"
                        d={wirePath(nodeCenter(component.promptBox), terminal)}
                      />
                      <path
                        className="atlas-wire-cluster"
                        d={wirePath(terminal, nodeCenter(component.infoBox))}
                      />
                    </g>
                  );
                })}
              </g>
            </CanvasWires>

            {board.components.map((component) => {
              const runState = componentOf(run, component.id);
              const gate = gateView(run, component.id);
              const editable = !readOnly && showsConfiguration(runState);
              const stage = stageControls(run, component.id);
              const progress = committeeProgress(run, component.id, component.seats.length);
              const statusClass = runState === null ? '' : `atlas-node-status-${runState.status}`;
              // A seat bound to a pipeline stage keeps that stage's accent, so a
              // migrated board still reads the way it did before.
              const kindClass =
                component.legacyRole === null ? '' : `atlas-node-kind-${component.legacyRole}`;
              const artifacts = (run?.artifacts ?? []).filter(
                (record) => record.producer.componentId === component.id,
              );
              // Publication belongs to the stage that opened the publish gate,
              // so an unrelated seat never claims the pull request as its own.
              const ownsPublication = run?.gate?.componentId === component.id;

              return (
                <div key={component.id} className="contents">
                  <CanvasNode
                    {...nodeChrome(component.id, component.title, TerminalIcon)}
                    className={cn('atlas-node-seat-terminal', kindClass, statusClass)}
                    meta={
                      <span className="text-muted-foreground text-[10px]">
                        {component.seats.length > 1
                          ? `${component.seats.length} seats`
                          : `${component.seat.vendor} · ${component.seat.model}`}
                      </span>
                    }
                    onRename={editable ? (title) => updateComponent(component.id, { title }) : undefined}
                  >
                    <CommitteeTerminalPane
                      componentId={component.id}
                      runState={runState}
                      visible={showsSeatTerminal(runState)}
                      now={props.now}
                      selectedAttemptId={selectedAttempts[component.id] ?? null}
                      terminals={props.terminals}
                      controlsFor={(attemptId) =>
                        attemptControls(run, attemptId, {
                          connected: props.terminals[attemptId]?.snapshot?.status === 'open',
                        })
                      }
                      onSelectAttempt={(attemptId) =>
                        setSelectedAttempts((current) => ({ ...current, [component.id]: attemptId }))
                      }
                      onInput={props.onTerminalInput}
                      onReconnect={props.onReconnectTerminal}
                      onTakeControl={props.onTakeover}
                      onHandBack={props.onHandback}
                      onRetry={() => props.onRetryStage(component.id)}
                      onCancel={props.onCancelRun}
                      canRetry={stage.canRetry}
                      canCancel={stage.canCancel}
                    />
                  </CanvasNode>

                  <CanvasNode
                    {...nodeChrome(seatPromptNodeId(component.id), 'PROMPT', MessageSquareIcon)}
                    className="atlas-node-seat-prompt"
                  >
                    <SeatPromptPane
                      component={component}
                      editable={editable}
                      canSend={sendTarget !== null}
                      draft={drafts[component.id] ?? ''}
                      onPromptChange={(prompt) => updateComponent(component.id, { prompt })}
                      onDraftChange={(draft) =>
                        setDrafts((current) => ({ ...current, [component.id]: draft }))
                      }
                      onSend={async (text) => {
                        // Resolved at send time, not at render: control can be
                        // handed back between typing and pressing Enter.
                        const target = controllableAttemptId();
                        if (target === null) throw new Error('No seat terminal is under your control.');
                        await props.onSendToAttempt(target, text);
                      }}
                    />
                  </CanvasNode>

                  <CanvasNode
                    {...nodeChrome(seatInfoNodeId(component.id), 'INFO', InfoIcon)}
                    className={cn('atlas-node-seat-info', statusClass)}
                  >
                    <div className="atlas-card" onClick={(event) => event.stopPropagation()}>
                      {runState !== null && <RunStateStrip runState={runState} />}
                      {runState !== null && component.seats.length > 1 && (
                        <CommitteeProgressStrip progress={progress} />
                      )}

                      <TriggerGatePane gate={gate} onDecide={props.onDecideGate} />
                      <RepairGatePane gate={gate} onDecide={props.onDecideGate} />
                      {(gate.reason === 'blocked_by_gate' ||
                        (ownsPublication && run?.publication != null)) && (
                        <PublishGatePane
                          gate={gate}
                          publication={ownsPublication ? (run?.publication ?? null) : null}
                          unavailableReason={publicationUnavailableReason(run)}
                          onReadPreflight={props.onReadPreflight}
                          onDecide={props.onDecideGate}
                        />
                      )}

                      <CommitteeEditor
                        component={component}
                        editable={editable}
                        onResize={(size) => props.onBoardChange(setCommitteeSize(board, component.id, size))}
                        onWorkerChange={(workerId, seat) =>
                          props.onBoardChange(setWorkerSeat(board, component.id, workerId, seat))
                        }
                        onConsolidationPromptChange={(consolidationPrompt) =>
                          updateComponent(component.id, { consolidationPrompt })
                        }
                      />

                      <div className="atlas-outputs">
                        <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
                          Produces
                        </span>
                        {component.requiredOutputs.map((output) => (
                          <span key={output} className="atlas-output">
                            {output}
                          </span>
                        ))}
                      </div>

                      {artifacts.length > 0 && (
                        <div className="flex flex-col gap-0.5">
                          <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
                            Artifacts
                          </span>
                          {artifacts.map((record) => (
                            <button
                              key={record.artifactId}
                              type="button"
                              className="hover:bg-muted flex items-center gap-1.5 rounded px-1 py-0.5 text-left text-[11px]"
                              onClick={() =>
                                openArtifact(record.name, record.producer.seatId ?? component.id)
                              }
                            >
                              <FileTextIcon className="size-3 shrink-0" />
                              <span className="flex-1 truncate font-mono">{record.name}</span>
                              {record.producer.seatId !== undefined && (
                                <span className="text-muted-foreground font-mono text-[10px]">
                                  {record.producer.seatId}
                                </span>
                              )}
                            </button>
                          ))}
                        </div>
                      )}

                      {!readOnly && (
                        <div className="flex flex-wrap items-center gap-1 pt-1">
                          <Button
                            size="xs"
                            variant={linkFrom === component.id ? 'secondary' : 'ghost'}
                            aria-label={`Connect from ${component.title}`}
                            title="Click a source seat, then its target"
                            onClick={() => onLinkClick(component.id)}
                          >
                            <WaypointsIcon />
                            {linkFrom === null
                              ? 'Connect'
                              : linkFrom === component.id
                                ? 'Pick a target…'
                                : 'Connect here'}
                          </Button>
                          <Button
                            size="xs"
                            variant="ghost"
                            className="text-muted-foreground hover:text-destructive"
                            aria-label={`Remove ${component.title}`}
                            disabled={board.components.length <= 1}
                            onClick={() => {
                              props.onBoardChange(removeComponent(board, component.id));
                              if (linkFrom === component.id) setLinkFrom(null);
                            }}
                          >
                            <Trash2Icon />
                            Remove seat
                          </Button>
                        </div>
                      )}
                    </div>
                  </CanvasNode>
                </div>
              );
            })}
          </CanvasWorldLayer>

          {showRail && run !== null && (
            <RunRail
              run={run}
              stages={railStages}
              gate={railGate}
              publicationUnavailable={publicationUnavailableReason(run)}
              canCancel={stageControls(run, '').canCancel}
              onReadPreflight={props.onReadPreflight}
              onDecide={props.onDecideGate}
              onCancel={props.onCancelRun}
            />
          )}

          <ZoomControls
            zoom={zoom}
            bounds={ATLAS_ZOOM_BOUNDS}
            onChange={setZoomPersisted}
            onReset={fitGraph}
            resetLabel="Fit graph"
          />
        </CanvasStage>
      </div>

      <ProjectDialog
        open={projectOpen}
        initialPath={project?.path ?? ''}
        suggestions={props.projectSuggestions}
        busy={saveState === 'loading'}
        onOpenChange={setProjectOpen}
        onChoose={props.onChooseProject}
      />
      <SaveBoardDialog
        open={saveOpen}
        busy={saveState === 'saving'}
        onOpenChange={setSaveOpen}
        onSave={props.onSaveBoardAs}
      />
      <RunDialog
        open={runOpen}
        board={board}
        boardId={props.activeBoard?.id ?? null}
        boardName={props.activeBoard?.name ?? 'this workflow'}
        needsSave={props.activeBoard === null}
        starting={props.starting}
        onOpenChange={setRunOpen}
        onPreview={props.onPreviewRun}
        onStart={props.onStartRun}
      />
      {artifact !== null && (
        <ArtifactDialog
          open
          runId={artifact.runId}
          name={artifact.name}
          producer={artifact.producer}
          onOpenChange={(next) => !next && setArtifact(null)}
          onRead={() => props.onReadArtifact(artifact.name)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Connected container
// ---------------------------------------------------------------------------

export type AtlasCanvasProps = {
  sessions?: readonly Session[];
  /** Injectable transport for tests; defaults to the guarded `apiFetch`. */
  fetch?: AtlasFetch;
};

/**
 * Atlas Canvas, connected to the collector.
 *
 * The board session owns project/board state and autosave; the run session owns
 * run mirroring and polling. Both are framework-free stores, so the parts that
 * must not lose an edit are testable directly and this component only binds
 * them to React and to the terminal transport.
 */
export function AtlasCanvas({ sessions = [], fetch: fetchImpl }: AtlasCanvasProps) {
  const boardSession = useMemo(() => createAtlasBoardSession({ fetch: fetchImpl }), [fetchImpl]);
  const runSession = useMemo(() => createAtlasRunSession({ fetch: fetchImpl }), [fetchImpl]);
  const boardState = useSyncExternalStore(boardSession.subscribe, boardSession.snapshot);
  const runState = useSyncExternalStore(runSession.subscribe, runSession.snapshot);

  const [terminals, setTerminals] = useState<Record<string, AtlasTerminalView>>({});
  const [now, setNow] = useState(() => Date.now());
  const connections = useRef(new Map<string, TerminalConnection>());
  // Attempts whose attach is in flight. A committee renders many attempts at
  // once, so without this a re-render mid-attach opens a second socket for the
  // same seat and the operator types into whichever one won.
  const attaching = useRef(new Set<string>());

  const projectId = boardState.project?.id ?? null;
  const run = runState.activeRun;
  const runId = run?.runId ?? null;

  useEffect(() => () => boardSession.dispose(), [boardSession]);

  useEffect(() => {
    void runSession.setProject(projectId);
  }, [runSession, projectId]);

  // Disposal is bound to the store, not to the project: tearing the session
  // down on every project change would cancel the load it had just started.
  useEffect(() => () => runSession.dispose(), [runSession]);

  // Elapsed labels advance with the run, so the clock is refreshed whenever the
  // mirrored run changes rather than on a timer of its own.
  useEffect(() => setNow(Date.now()), [run]);

  const closeTerminal = useCallback((attemptId: string) => {
    connections.current.get(attemptId)?.close();
    connections.current.delete(attemptId);
    attaching.current.delete(attemptId);
    setTerminals((current) => ({ ...current, [attemptId]: { snapshot: null, error: '' } }));
  }, []);

  const attach = useCallback(
    async (attemptId: string, force = false) => {
      if (projectId === null || runId === null) return;
      if (force) closeTerminal(attemptId);
      else if (connections.current.has(attemptId) || attaching.current.has(attemptId)) return;
      attaching.current.add(attemptId);
      try {
        const handle = await reconnectAtlasTerminal(projectId, runId, attemptId, fetchImpl);
        // The attempt may have been closed while the handle was in flight.
        if (!attaching.current.has(attemptId)) return;
        const connection = createTerminalConnection({ terminalId: handle.terminalId });
        connections.current.set(attemptId, connection);
        const publish = () =>
          setTerminals((current) => ({
            ...current,
            [attemptId]: { snapshot: connection.snapshot(), error: '' },
          }));
        connection.subscribe(publish);
        publish();
      } catch (caught) {
        setTerminals((current) => ({
          ...current,
          [attemptId]: {
            snapshot: null,
            error: caught instanceof Error ? caught.message : 'The seat terminal is unavailable.',
          },
        }));
      } finally {
        attaching.current.delete(attemptId);
      }
    },
    [closeTerminal, fetchImpl, projectId, runId],
  );

  // Attach to every live attempt, not just the newest: a committee is several
  // turns at once, and watching only one would make the fan-out invisible at
  // exactly the moment it matters. Let go as soon as an attempt exits — a
  // terminal for an exited attempt is a socket held open for nothing.
  useEffect(() => {
    const live = new Set<string>();
    for (const component of Object.values(run?.components ?? {})) {
      for (const attempt of component.attempts ?? []) {
        if (attempt.status === 'exited') continue;
        live.add(attempt.attemptId);
        void attach(attempt.attemptId);
      }
    }
    for (const attemptId of [...connections.current.keys()]) {
      if (!live.has(attemptId)) closeTerminal(attemptId);
    }
  }, [attach, closeTerminal, run]);

  useEffect(() => {
    const open = connections.current;
    const pending = attaching.current;
    return () => {
      for (const connection of open.values()) connection.close();
      open.clear();
      pending.clear();
    };
  }, []);

  const adopt = useCallback((next: AtlasRun) => runSession.applyRun(next), [runSession]);

  function requireRun(): { projectId: string; runId: string } {
    if (projectId === null || runId === null) throw new Error('No run is being watched.');
    return { projectId, runId };
  }

  const onReadArtifact = useCallback(
    async (name: string) => {
      const target = requireRun();
      return readAtlasArtifact(target.projectId, target.runId, name, fetchImpl);
    },
    // `requireRun` closes over the current ids, so the identity must change with them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fetchImpl, projectId, runId],
  );

  const onReadPreflight = useCallback(async () => {
    const target = requireRun();
    return readAtlasPublishPreflight(target.projectId, target.runId, fetchImpl);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetchImpl, projectId, runId]);

  const onPreviewRun = useCallback(
    async (input: { boardId: string } | { board: AtlasBoard }) => {
      if (projectId === null) throw new Error('Choose a project first.');
      return previewAtlasRun(projectId, input, fetchImpl);
    },
    [fetchImpl, projectId],
  );

  const projectSuggestions = useMemo(
    () =>
      [...new Set(sessions.map((session) => session.cwd))]
        .filter((cwd): cwd is string => typeof cwd === 'string' && cwd.trim() !== '')
        .slice(0, 8),
    [sessions],
  );

  return (
    <AtlasBoardView
      board={boardState.board}
      project={boardState.project}
      boards={boardState.boards}
      activeBoard={boardState.activeBoard}
      saveState={boardState.saveState}
      boardError={boardState.error}
      readOnly={boardState.readOnly}
      migrated={boardState.migrated}
      run={run}
      runs={runState.runs}
      starting={runState.starting}
      runError={runState.error}
      terminals={terminals}
      projectSuggestions={projectSuggestions}
      now={now}
      onBoardChange={(next) => boardSession.edit(next)}
      onChooseProject={(path) => boardSession.chooseProject(path)}
      onOpenBoard={(summary) => void boardSession.openBoard(summary.id)}
      onDeleteBoard={(summary) => void boardSession.deleteBoard(summary)}
      onNewBoard={() => boardSession.newBoard()}
      onSaveBoardAs={async (name) => {
        await boardSession.saveAs(name);
      }}
      onKeepLocal={() => void boardSession.keepLocal()}
      onReloadFromServer={() => void boardSession.reloadFromServer()}
      onSelectRun={(id) => void runSession.selectRun(id)}
      onPreviewRun={onPreviewRun}
      onStartRun={async ({ source, boardName }) => {
        let id = boardState.activeBoard?.id;
        if (id === undefined) {
          if (boardName === undefined || boardName.trim() === '')
            throw new Error('Name this workflow before starting a run.');
          id = (await boardSession.saveAs(boardName.trim())).id;
        } else {
          // A run is pinned to a stored revision, so any pending edit must land
          // before the run reads the board from disk.
          await boardSession.flush();
        }
        await runSession.start(id, source);
      }}
      onReadArtifact={onReadArtifact}
      onReadPreflight={onReadPreflight}
      onDecideGate={async (decision, options) => {
        const target = requireRun();
        adopt(await decideAtlasGate(target.projectId, target.runId, decision, options ?? {}, fetchImpl));
      }}
      onRetryStage={async (componentId) => {
        const target = requireRun();
        adopt(await retryAtlasStage(target.projectId, target.runId, componentId, fetchImpl));
      }}
      onCancelRun={async () => {
        const target = requireRun();
        adopt(await cancelAtlasRun(target.projectId, target.runId, fetchImpl));
      }}
      onTakeover={async (attemptId) => {
        const target = requireRun();
        adopt(await takeoverAtlasAttempt(target.projectId, target.runId, attemptId, fetchImpl));
        // Takeover rebinds the attempt's provider session, so the old socket is
        // no longer the operator's terminal.
        await attach(attemptId, true);
      }}
      onHandback={async (attemptId) => {
        const target = requireRun();
        adopt(await handbackAtlasAttempt(target.projectId, target.runId, attemptId, fetchImpl));
      }}
      onTerminalInput={(attemptId, data) => {
        try {
          connections.current.get(attemptId)?.input(data);
        } catch {
          // A closed socket is already reported through the connection snapshot.
        }
      }}
      onReconnectTerminal={(attemptId) => void attach(attemptId, true)}
      onSendToAttempt={async (attemptId, text) => {
        const connection = connections.current.get(attemptId);
        if (connection === undefined) throw new Error('This seat terminal is not connected.');
        // Bracketed paste keeps a multi-line message one submission instead of
        // sending the first line and stranding the rest.
        connection.input(`${bracketedPaste(text)}\r`);
      }}
    />
  );
}

/** True once a run has finished, so callers can stop offering controls. */
export { isTerminalRun };
