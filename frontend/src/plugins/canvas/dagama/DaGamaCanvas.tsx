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
  CheckCircle2Icon,
  FileTextIcon,
  FolderOpenIcon,
  GitPullRequestIcon,
  HammerIcon,
  InfoIcon,
  MessageSquareIcon,
  PlayIcon,
  ScanSearchIcon,
  SearchCheckIcon,
  TerminalIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { Session } from '@/pages/coslash/lib/session';
import {
  cancelDaGamaRun,
  decideDaGamaGate,
  handbackDaGamaSeat,
  previewDaGamaRun,
  readDaGamaPublishPreflight,
  readDaGamaRunArtifact,
  readDaGamaRunPrompt,
  reconnectDaGamaTerminal,
  retryDaGamaSeat,
  takeoverDaGamaSeat,
  type DaGamaBoardSummary,
  type DaGamaFetch,
} from '@/plugins/canvas/dagama/api';
import {
  applyLayoutUpdates,
  boardContentExtent,
  DAGAMA_COMPONENT_META,
  DAGAMA_FLOW,
  DAGAMA_NODE_MIN_HEIGHT,
  DAGAMA_NODE_MIN_WIDTH,
  DAGAMA_REPAIR_FLOW,
  DAGAMA_WORLD,
  DAGAMA_ZOOM_BOUNDS,
  layoutForNode,
  seatInfoNodeId,
  seatPromptNodeId,
  withComponent,
  type DaGamaBoard,
  type DaGamaComponent,
  type DaGamaNodeId,
} from '@/plugins/canvas/dagama/board';
import {
  ArtifactDialog,
  BoardMenu,
  ProjectDialog,
  RunChip,
  RunDialog,
  SaveBoardDialog,
} from '@/plugins/canvas/dagama/dialogs';
import { ComponentCard, SeatInfoPane, SeatPromptPane, SeatTerminalPane } from '@/plugins/canvas/dagama/panes';
import { readLastProjectPath } from '@/plugins/canvas/dagama/preferences';
import { createDaGamaRunSession } from '@/plugins/canvas/dagama/run-session';
import {
  componentOf,
  gateView,
  isTerminalRun,
  runBlockedReason,
  seatControls,
  showsConfiguration,
  showsSeatTerminal,
  type DaGamaSeatControls,
} from '@/plugins/canvas/dagama/runs';
import { createDaGamaBoardSession, type DaGamaSaveState } from '@/plugins/canvas/dagama/session';
import {
  bracketedPaste,
  createTerminalConnection,
  type TerminalConnection,
  type TerminalConnectionSnapshot,
} from '@/plugins/canvas/dagama/terminal';
import type {
  DaGamaProject,
  DaGamaPublishPreflight,
  DaGamaRun,
  DaGamaRunPreview,
  DaGamaRunSourceInput,
  DaGamaRunSummary,
} from '@/plugins/canvas/dagama/types';
import {
  DAGAMA_COMPONENT_IDS,
  DAGAMA_SEAT_COMPONENT_IDS,
  hasSeat,
  isDaGamaSeatComponentId,
  type DaGamaComponentId,
  type DaGamaSeatComponentId,
} from '@/plugins/canvas/dagama/vocabulary';
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
import '@/plugins/canvas/dagama/dagama.css';

const COMPONENT_ICONS: Record<DaGamaComponentId, ComponentType<{ className?: string }>> = {
  intake: FileTextIcon,
  plan: ScanSearchIcon,
  build: HammerIcon,
  verify: SearchCheckIcon,
  review: CheckCircle2Icon,
  publish: GitPullRequestIcon,
};

function headerSummary(component: DaGamaComponent): string {
  if (component.seat) return `${component.seat.vendor} · ${component.seat.model}`;
  if (component.id === 'verify')
    return component.checks.length === 0
      ? 'no checks — skipped'
      : `${component.checks.length} ${component.checks.length === 1 ? 'check' : 'checks'}`;
  if (component.publish) return component.publish.base || 'default branch';
  return 'deterministic';
}

export type DaGamaTerminalView = {
  snapshot: TerminalConnectionSnapshot | null;
  error: string;
};

export type DaGamaBoardViewProps = {
  board: DaGamaBoard;
  project: DaGamaProject | null;
  boards: readonly DaGamaBoardSummary[];
  activeBoard: DaGamaBoardSummary | null;
  saveState: DaGamaSaveState;
  boardError: string;
  run: DaGamaRun | null;
  runs: readonly DaGamaRunSummary[];
  starting: boolean;
  runError: string;
  terminals: Partial<Record<DaGamaSeatComponentId, DaGamaTerminalView>>;
  projectSuggestions: readonly string[];
  /** Clock for elapsed labels, supplied by the caller so rendering stays pure. */
  now: number;
  onBoardChange: (board: DaGamaBoard) => void;
  onChooseProject: (path: string) => Promise<void>;
  onOpenBoard: (summary: DaGamaBoardSummary) => void;
  onDeleteBoard: (summary: DaGamaBoardSummary) => void;
  onNewBoard: () => void;
  onSaveBoardAs: (name: string) => Promise<void>;
  onKeepLocal: () => void;
  onReloadFromServer: () => void;
  onSelectRun: (runId: string | null) => void;
  onPreviewRun: (input: { boardId: string } | { board: DaGamaBoard }) => Promise<DaGamaRunPreview>;
  onStartRun: (input: { source: DaGamaRunSourceInput; boardName?: string }) => Promise<void>;
  onReadArtifact: (name: string) => Promise<string>;
  onReadPrompt: (componentId: DaGamaSeatComponentId) => Promise<string>;
  onReadPreflight: () => Promise<DaGamaPublishPreflight>;
  onDecideGate: (decision: 'approved' | 'rejected', options?: { publish?: boolean }) => Promise<void>;
  onRetrySeat: (componentId: DaGamaSeatComponentId) => Promise<void>;
  onCancelRun: (componentId: DaGamaSeatComponentId) => Promise<void>;
  onTakeover: (componentId: DaGamaSeatComponentId) => Promise<void>;
  onHandback: (componentId: DaGamaSeatComponentId) => Promise<void>;
  onTerminalInput: (componentId: DaGamaSeatComponentId, data: string) => void;
  onTerminalResize: (componentId: DaGamaSeatComponentId, cols: number, rows: number) => void;
  onReconnectTerminal: (componentId: DaGamaSeatComponentId) => void;
  onSendToSeat: (componentId: DaGamaSeatComponentId, text: string) => Promise<void>;
};

/**
 * The DaGama board.
 *
 * Presentational: every effect that talks to the collector is a prop, so the
 * pipeline, its controls, and its gating can be rendered and asserted without a
 * server. `DaGamaCanvas` below is the connected container.
 */
export function DaGamaBoardView(props: DaGamaBoardViewProps) {
  const { board, project, run, saveState } = props;
  const components = board.components;
  const [selected, setSelected] = useState<DaGamaNodeId | null>(null);
  const [zoom, setZoom] = useState(board.viewport.zoom);
  const [projectOpen, setProjectOpen] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [artifact, setArtifact] = useState<{ runId: string; name: string } | null>(null);
  const stageRef = useRef<HTMLDivElement>(null);

  const interaction = useCanvasNodeInteraction<DaGamaNodeId>({
    zoom,
    disabled: false,
    world: DAGAMA_WORLD,
    minWidth: DAGAMA_NODE_MIN_WIDTH,
    minHeight: DAGAMA_NODE_MIN_HEIGHT,
    getLayout: (id) => layoutForNode(components, id),
    updateLayouts: (updates) => props.onBoardChange(applyLayoutUpdates(board, updates)),
    onSelect: setSelected,
    // Dragging a seat terminal carries its prompt and info companions.
    getCompanions: (id) => (isDaGamaSeatComponentId(id) ? [seatPromptNodeId(id), seatInfoNodeId(id)] : []),
  });

  const setZoomPersisted = (next: number) => {
    const clamped = clampZoom(DAGAMA_ZOOM_BOUNDS, next);
    setZoom(clamped);
    props.onBoardChange({ ...board, viewport: { ...board.viewport, zoom: clamped } });
  };

  const fitWorkflow = () => {
    const rect = stageRef.current?.getBoundingClientRect();
    if (rect === undefined) return;
    // Fit the content the board actually draws, not the whole world, so a board
    // whose stages sit on the left rail does not zoom out to nothing.
    setZoomPersisted(
      fitZoom(DAGAMA_ZOOM_BOUNDS, boardContentExtent(board), {
        width: rect.width - 64,
        height: rect.height - 96,
      }),
    );
  };

  const onBoardKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (isTextEntryTarget(event.target as HTMLElement)) return;
    const command = boardCommandFor(event);
    if (command === null) return;
    if (command === 'exit-focus') {
      event.preventDefault();
      setSelected(null);
      return;
    }
    if (command === 'zoom-in' || command === 'zoom-out' || command === 'zoom-reset') {
      event.preventDefault();
      if (command === 'zoom-reset') fitWorkflow();
      else
        setZoomPersisted(zoom + (command === 'zoom-in' ? DAGAMA_ZOOM_BOUNDS.step : -DAGAMA_ZOOM_BOUNDS.step));
    }
  };

  const updateComponent = (id: DaGamaComponentId, patch: Partial<DaGamaComponent>) =>
    props.onBoardChange(withComponent(board, id, patch));

  const blockedReason = runBlockedReason({ hasProject: project !== null, saveState });
  const conflict = saveState === 'conflict';

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

  const openArtifact = (name: string) => {
    if (run !== null) setArtifact({ runId: run.runId, name });
  };

  const nodeChrome = (id: DaGamaNodeId, title: string, icon: ComponentType<{ className?: string }>) => ({
    id,
    title,
    icon,
    layout: layoutForNode(components, id),
    selected: selected === id,
    focused: false,
    focusActive: false,
    onSelect: setSelected,
    // The scrolling shared stage has no pan, so focus scrolls the node into view.
    onFocus: (node: DaGamaNodeId) => {
      setSelected(node);
      document.querySelector(`.canvas-node-${node}`)?.scrollIntoView({ block: 'center', inline: 'center' });
    },
    onToggleCollapse: (node: DaGamaNodeId) =>
      props.onBoardChange(
        applyLayoutUpdates(board, [[node, (layout) => ({ ...layout, collapsed: !layout.collapsed })]]),
      ),
    onToggleLock: (node: DaGamaNodeId) =>
      props.onBoardChange(
        applyLayoutUpdates(board, [[node, (layout) => ({ ...layout, locked: !layout.locked })]]),
      ),
    onDragStart: interaction.onDragStart,
    onResizeStart: interaction.onResizeStart,
  });

  return (
    <div className="dagama-board" onKeyDown={onBoardKeyDown}>
      <div ref={stageRef} className="relative min-h-0 flex-1">
        <CanvasStage
          toolbar={
            <>
              <div className="flex items-center gap-2">
                <div className="dagama-toolbar-group">
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
                    }}
                    onSaveAs={() => setSaveOpen(true)}
                  />
                </div>
                <div className="dagama-toolbar-group">
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
              <div className="dagama-status" role="status">
                <span
                  className={cn('size-1.5 rounded-full', {
                    'bg-success': saveState === 'saved' && props.runError === '',
                    'bg-warning': saveState === 'saving' || saveState === 'loading',
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
          <CanvasWorldLayer world={DAGAMA_WORLD} zoom={zoom} hasFocus={false}>
            <CanvasWires world={DAGAMA_WORLD}>
              <g className="dagama-wires">
                {DAGAMA_FLOW.map(([from, to]) => (
                  <path
                    key={`flow-${from}-${to}`}
                    className={cn('dagama-wire-flow', {
                      'dagama-wire-active': componentOf(run, to)?.status === 'running',
                    })}
                    d={triggerWirePath(components[from].box, components[to].box)}
                  />
                ))}
                {DAGAMA_REPAIR_FLOW.map(([from, to]) => (
                  <path
                    key={`repair-${from}-${to}`}
                    className="dagama-wire-repair"
                    d={repairPath(components, from, to)}
                  />
                ))}
                {DAGAMA_SEAT_COMPONENT_IDS.map((id) => {
                  const component = components[id];
                  if (!component.promptBox || !component.infoBox) return null;
                  const terminal = nodeCenter(component.box);
                  return (
                    <g key={`cluster-${id}`}>
                      <path
                        className="dagama-wire-cluster"
                        d={wirePath(nodeCenter(component.promptBox), terminal)}
                      />
                      <path
                        className="dagama-wire-cluster"
                        d={wirePath(terminal, nodeCenter(component.infoBox))}
                      />
                    </g>
                  );
                })}
              </g>
            </CanvasWires>

            {DAGAMA_COMPONENT_IDS.map((id) => {
              const component = components[id];
              const runState = componentOf(run, id);
              const gate = gateView(run, id);
              const statusClass = runState ? `dagama-node-status-${runState.status}` : '';
              const editable = showsConfiguration(runState);

              if (!hasSeat(id)) {
                return (
                  <CanvasNode
                    key={id}
                    {...nodeChrome(id, DAGAMA_COMPONENT_META[id].title, COMPONENT_ICONS[id])}
                    className={cn(`dagama-node-kind-${id}`, statusClass)}
                    meta={
                      <span className="text-muted-foreground text-[10px]">{headerSummary(component)}</span>
                    }
                  >
                    <ComponentCard
                      component={component}
                      runState={runState}
                      gate={gate}
                      publication={run?.publication ?? null}
                      editable={editable}
                      onChange={(patch) => updateComponent(id, patch)}
                      onOpenArtifact={run !== null ? openArtifact : undefined}
                      onReadArtifact={run !== null ? props.onReadArtifact : undefined}
                      onReadPreflight={run !== null ? props.onReadPreflight : undefined}
                      onDecideGate={run !== null ? props.onDecideGate : undefined}
                    />
                  </CanvasNode>
                );
              }

              const seatId = id as DaGamaSeatComponentId;
              const controls: DaGamaSeatControls = seatControls(run, seatId, {
                connected: props.terminals[seatId]?.snapshot?.status === 'open',
              });
              if (!component.promptBox || !component.infoBox) return null;

              return (
                <div key={id} className="contents">
                  <CanvasNode
                    {...nodeChrome(seatId, DAGAMA_COMPONENT_META[id].title, TerminalIcon)}
                    className={cn(`dagama-node-kind-${id}`, 'dagama-node-seat-terminal', statusClass)}
                    meta={
                      <span className="text-muted-foreground text-[10px]">{headerSummary(component)}</span>
                    }
                  >
                    <SeatTerminalPane
                      componentId={seatId}
                      runState={runState}
                      controls={controls}
                      terminal={props.terminals[seatId]?.snapshot ?? null}
                      attachError={props.terminals[seatId]?.error ?? ''}
                      visible={showsSeatTerminal(runState)}
                      now={props.now}
                      onInput={(data) => props.onTerminalInput(seatId, data)}
                      onResize={(cols, rows) => props.onTerminalResize(seatId, cols, rows)}
                      onReconnect={() => props.onReconnectTerminal(seatId)}
                      onRetry={() => props.onRetrySeat(seatId)}
                      onCancel={() => props.onCancelRun(seatId)}
                      onTakeControl={() => props.onTakeover(seatId)}
                      onHandBack={() => props.onHandback(seatId)}
                    />
                  </CanvasNode>

                  <CanvasNode
                    {...nodeChrome(seatPromptNodeId(seatId), 'PROMPT', MessageSquareIcon)}
                    className={cn(`dagama-node-kind-${id}`, 'dagama-node-seat-prompt')}
                  >
                    <SeatPromptPane
                      componentId={seatId}
                      boardPrompt={component.prompt}
                      draft={component.promptDraft}
                      editable={editable}
                      controls={controls}
                      hasAttempt={runState?.attempt != null}
                      onBoardPromptChange={(prompt) => updateComponent(seatId, { prompt })}
                      onDraftChange={(promptDraft) => updateComponent(seatId, { promptDraft })}
                      onSend={(text) => props.onSendToSeat(seatId, text)}
                      onReadPrompt={run !== null ? () => props.onReadPrompt(seatId) : undefined}
                    />
                  </CanvasNode>

                  <CanvasNode
                    {...nodeChrome(seatInfoNodeId(seatId), 'INFO', InfoIcon)}
                    className={cn(`dagama-node-kind-${id}`, 'dagama-node-seat-info', statusClass)}
                  >
                    <SeatInfoPane
                      component={component}
                      runState={runState}
                      gate={gate}
                      editable={editable}
                      onChange={(patch) => updateComponent(seatId, patch)}
                      onOpenArtifact={run !== null ? openArtifact : undefined}
                      onDecideGate={run !== null ? props.onDecideGate : undefined}
                    />
                  </CanvasNode>
                </div>
              );
            })}
          </CanvasWorldLayer>

          <ZoomControls
            zoom={zoom}
            bounds={DAGAMA_ZOOM_BOUNDS}
            onChange={setZoomPersisted}
            onReset={fitWorkflow}
            resetLabel="Fit workflow"
          />
        </CanvasStage>
      </div>

      <ProjectDialog
        open={projectOpen}
        initialPath={project?.path ?? readLastProjectPath()}
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

      {project !== null && (
        <RunDialog
          open={runOpen}
          board={board}
          boardId={props.activeBoard?.id ?? null}
          boardName={props.activeBoard?.name ?? 'Untitled workflow'}
          needsSave={props.activeBoard === null}
          starting={props.starting}
          onOpenChange={setRunOpen}
          onPreview={props.onPreviewRun}
          onStart={props.onStartRun}
        />
      )}

      {artifact !== null && (
        <ArtifactDialog
          open
          runId={artifact.runId}
          name={artifact.name}
          onOpenChange={(next) => !next && setArtifact(null)}
          onRead={() => props.onReadArtifact(artifact.name)}
        />
      )}
    </div>
  );
}

/**
 * The repair wire.
 *
 * It dips below the whole seat cluster rather than taking the direct route, so
 * the return edge never crosses the terminals it is returning past.
 */
function repairPath(
  components: DaGamaBoard['components'],
  from: DaGamaComponentId,
  to: DaGamaComponentId,
): string {
  const start = nodeCenter(components[from].box);
  const end = nodeCenter(components[to].box);
  const source = components[from];
  const dip =
    Math.max(
      source.box.y + source.box.height,
      source.promptBox ? source.promptBox.y + source.promptBox.height : 0,
      source.infoBox ? source.infoBox.y + source.infoBox.height : 0,
      start.y,
    ) + 220;
  return `M${start.x} ${start.y} C${start.x} ${dip} ${end.x} ${dip} ${end.x} ${end.y}`;
}

// ---------------------------------------------------------------------------
// Connected container
// ---------------------------------------------------------------------------

type SeatTerminalState = Partial<Record<DaGamaSeatComponentId, DaGamaTerminalView>>;

export type DaGamaCanvasProps = {
  sessions?: readonly Session[];
  /** Injectable transport for tests; defaults to the guarded `apiFetch`. */
  fetch?: DaGamaFetch;
};

/**
 * DaGama Canvas, connected to the collector.
 *
 * The board session owns project/board state and autosave; the run session owns
 * run mirroring and polling. Both are framework-free stores so the parts that
 * must not lose an edit are testable directly, and this component only binds
 * them to React and to the terminal transport.
 */
export function DaGamaCanvas({ sessions = [], fetch: fetchImpl }: DaGamaCanvasProps) {
  const boardSession = useMemo(() => createDaGamaBoardSession({ fetch: fetchImpl }), [fetchImpl]);
  const runSession = useMemo(() => createDaGamaRunSession({ fetch: fetchImpl }), [fetchImpl]);
  const boardState = useSyncExternalStore(boardSession.subscribe, boardSession.snapshot);
  const runState = useSyncExternalStore(runSession.subscribe, runSession.snapshot);

  const [terminals, setTerminals] = useState<SeatTerminalState>({});
  const [now, setNow] = useState(() => Date.now());
  const connections = useRef(new Map<DaGamaSeatComponentId, TerminalConnection>());
  const attached = useRef(new Map<DaGamaSeatComponentId, string>());

  const projectId = boardState.project?.id ?? null;
  const run = runState.activeRun;
  const runId = run?.runId ?? null;

  useEffect(() => {
    void boardSession.restore();
    return () => boardSession.dispose();
  }, [boardSession]);

  useEffect(() => {
    void runSession.setProject(projectId);
  }, [runSession, projectId]);

  // Disposal is bound to the store, not to the project: tearing the session
  // down on every project change would cancel the load it had just started.
  useEffect(() => () => runSession.dispose(), [runSession]);

  // Elapsed labels advance with the run, so the clock is refreshed whenever the
  // mirrored run changes rather than on a timer of its own.
  useEffect(() => setNow(Date.now()), [run]);

  const closeTerminal = useCallback((seat: DaGamaSeatComponentId) => {
    connections.current.get(seat)?.close();
    connections.current.delete(seat);
    attached.current.delete(seat);
    setTerminals((current) => ({ ...current, [seat]: { snapshot: null, error: '' } }));
  }, []);

  const attach = useCallback(
    async (seat: DaGamaSeatComponentId, force = false) => {
      if (projectId === null || runId === null) return;
      const attempt = run?.components?.[seat]?.attempt ?? null;
      if (attempt === null) return;
      if (!force && attached.current.get(seat) === attempt.attemptId) return;
      closeTerminal(seat);
      attached.current.set(seat, attempt.attemptId);
      try {
        const handle = await reconnectDaGamaTerminal(projectId, runId, seat, fetchImpl);
        const connection = createTerminalConnection({ terminalId: handle.terminalId });
        connections.current.set(seat, connection);
        const publish = () =>
          setTerminals((current) => ({
            ...current,
            [seat]: { snapshot: connection.snapshot(), error: '' },
          }));
        connection.subscribe(publish);
        publish();
      } catch (caught) {
        attached.current.delete(seat);
        setTerminals((current) => ({
          ...current,
          [seat]: {
            snapshot: null,
            error: caught instanceof Error ? caught.message : 'The seat terminal is unavailable.',
          },
        }));
      }
    },
    [closeTerminal, fetchImpl, projectId, run, runId],
  );

  // Attach to a seat's terminal as soon as it has a live attempt, and let go as
  // soon as it does not: a terminal for an exited attempt is a socket held open
  // for nothing.
  useEffect(() => {
    for (const seat of DAGAMA_SEAT_COMPONENT_IDS) {
      const attempt = run?.components?.[seat]?.attempt ?? null;
      if (attempt === null || attempt.status === 'exited') {
        if (attached.current.has(seat)) closeTerminal(seat);
        continue;
      }
      void attach(seat);
    }
  }, [attach, closeTerminal, run]);

  useEffect(() => {
    const open = connections.current;
    return () => {
      for (const connection of open.values()) connection.close();
      open.clear();
    };
  }, []);

  const adopt = useCallback((next: DaGamaRun) => runSession.applyRun(next), [runSession]);

  function requireRun(): { projectId: string; runId: string } {
    if (projectId === null || runId === null) throw new Error('No run is being watched.');
    return { projectId, runId };
  }

  const onReadArtifact = useCallback(
    async (name: string) => {
      const target = requireRun();
      return readDaGamaRunArtifact(target.projectId, target.runId, name, fetchImpl);
    },
    // `requireRun` closes over the current ids, so the identity must change with them.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [fetchImpl, projectId, runId],
  );

  const onReadPreflight = useCallback(async () => {
    const target = requireRun();
    return readDaGamaPublishPreflight(target.projectId, target.runId, fetchImpl);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fetchImpl, projectId, runId]);

  const onPreviewRun = useCallback(
    async (input: { boardId: string } | { board: DaGamaBoard }) => {
      if (projectId === null) throw new Error('Choose a project first.');
      return previewDaGamaRun(projectId, input, fetchImpl);
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
    <DaGamaBoardView
      board={boardState.board}
      project={boardState.project}
      boards={boardState.boards}
      activeBoard={boardState.activeBoard}
      saveState={boardState.saveState}
      boardError={boardState.error}
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
      onReadPrompt={async (componentId) => {
        const target = requireRun();
        return readDaGamaRunPrompt(target.projectId, target.runId, componentId, fetchImpl);
      }}
      onReadPreflight={onReadPreflight}
      onDecideGate={async (decision, options) => {
        const target = requireRun();
        adopt(await decideDaGamaGate(target.projectId, target.runId, decision, options ?? {}, fetchImpl));
      }}
      onRetrySeat={async (componentId) => {
        const target = requireRun();
        adopt(await retryDaGamaSeat(target.projectId, target.runId, componentId, fetchImpl));
      }}
      onCancelRun={async (componentId) => {
        const target = requireRun();
        adopt(await cancelDaGamaRun(target.projectId, target.runId, componentId, fetchImpl));
      }}
      onTakeover={async (componentId) => {
        const target = requireRun();
        adopt(await takeoverDaGamaSeat(target.projectId, target.runId, componentId, fetchImpl));
        // Takeover replaces the attempt, so the old socket is no longer the
        // operator's terminal.
        await attach(componentId, true);
      }}
      onHandback={async (componentId) => {
        const target = requireRun();
        adopt(await handbackDaGamaSeat(target.projectId, target.runId, componentId, fetchImpl));
      }}
      onTerminalInput={(componentId, data) => {
        try {
          connections.current.get(componentId)?.input(data);
        } catch {
          // A closed socket is already reported through the connection snapshot.
        }
      }}
      onTerminalResize={(componentId, cols, rows) => {
        try {
          connections.current.get(componentId)?.resize(cols, rows);
        } catch {
          // Same: the snapshot already carries the connection state.
        }
      }}
      onReconnectTerminal={(componentId) => void attach(componentId, true)}
      onSendToSeat={async (componentId, text) => {
        const connection = connections.current.get(componentId);
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
