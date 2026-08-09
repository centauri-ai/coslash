import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type KeyboardEvent,
  type ReactNode,
} from 'react';
import {
  AlertTriangleIcon,
  BellIcon,
  BotIcon,
  BracesIcon,
  CheckCircle2Icon,
  CheckIcon,
  ClipboardListIcon,
  DownloadIcon,
  FileDiffIcon,
  FilesIcon,
  GitForkIcon,
  LightbulbIcon,
  ListRestartIcon,
  LoaderCircleIcon,
  MapIcon,
  MessageSquareTextIcon,
  MessagesSquareIcon,
  PencilIcon,
  PinIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  SendIcon,
  SparklesIcon,
  StickyNoteIcon,
  TerminalIcon,
  XIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { getStatus, getTotalTokens, getVendor, STATUSES } from '@/pages/coslash/lib/session';
import { createWorkspaceClient, type WorkspaceSnapshot } from '@/plugins/canvas/api/persistence';
import type { CanvasSessionIdentity } from '@/plugins/canvas/contracts';
import {
  analyzeTurn,
  bracketedPaste,
  forkSession,
  launchSessionTerminal,
  loadSessionDetail,
  loadSessionFile,
  renameSession,
  sendTerminalInput,
  stopTerminal,
  type SessionFetch,
  type SessionFilePreview,
} from '@/plugins/canvas/session/api';
import {
  createTerminalConnection,
  terminalKeyData,
  type TerminalConnectionSnapshot,
} from '@/plugins/canvas/session/terminal';
import type {
  SessionCanvasDetail,
  SessionCanvasWorkspace,
  SessionCheckpoint,
  SessionExperiment,
  SessionNodeId,
  TurnAnalysis,
} from '@/plugins/canvas/session/types';
import {
  autoArrangeSessionLayout,
  createCheckpoint,
  defaultSessionWorkspace,
  normalizeSessionWorkspace,
  reduceSessionLayoutUpdates,
  reduceSessionWorkspace,
  reduceWorkspaceForIdentity,
  SESSION_CANVAS_WORLD,
  SESSION_NODE_MIN_HEIGHT,
  SESSION_NODE_MIN_WIDTH,
  sessionAttention,
  sessionKey,
  sessionPinCandidates,
  staleSessionPinIds,
} from '@/plugins/canvas/session/workspace';
import {
  boardCommandFor,
  CANVAS_DEFAULT_ZOOM_BOUNDS,
  CanvasCommandOverlay,
  CanvasInspector,
  CanvasNode,
  CanvasSidePanel,
  CanvasStage,
  CanvasWires,
  CanvasWorldLayer,
  isTextEntryTarget,
  triggerWirePath,
  useCanvasNodeInteraction,
  ZoomControls,
} from '@/plugins/canvas/shared';
import '@/plugins/canvas/session/session.css';

type Panel = 'attention' | 'checkpoints' | 'pins' | 'compare' | null;

function Metric({ children }: { children: ReactNode }) {
  return <span className="session-canvas-metric">{children}</span>;
}

function Empty({ children }: { children: ReactNode }) {
  return <div className="session-canvas-empty">{children}</div>;
}

function SessionNameEditor({
  name,
  fallback,
  onRename,
}: {
  name: string | null;
  fallback: string;
  onRename: (name: string) => void;
}) {
  const visibleName = name?.trim() || fallback;
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(visibleName);

  useEffect(() => {
    if (!editing) setDraft(visibleName);
  }, [editing, visibleName]);

  if (!editing)
    return (
      <button
        type="button"
        className="session-canvas-name"
        onClick={(event) => {
          event.stopPropagation();
          setEditing(true);
        }}
        aria-label="Rename session"
      >
        <span>{visibleName}</span>
        <PencilIcon />
      </button>
    );

  return (
    <form
      className="session-canvas-name-editor"
      onClick={(event) => event.stopPropagation()}
      onSubmit={(event) => {
        event.preventDefault();
        const next = draft.trim();
        if (next !== '' && next !== visibleName) onRename(next);
        setEditing(false);
      }}
    >
      <input
        autoFocus
        value={draft}
        maxLength={200}
        onChange={(event) => setDraft(event.target.value)}
        aria-label="Session name"
      />
      <Button type="submit" variant="ghost" size="icon-xs" aria-label="Save session name">
        <CheckIcon />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={() => {
          setDraft(visibleName);
          setEditing(false);
        }}
        aria-label="Cancel session rename"
      >
        <XIcon />
      </Button>
    </form>
  );
}

function SessionOverview({
  detail,
  onRename,
}: {
  detail: SessionCanvasDetail;
  onRename: (name: string) => void;
}) {
  const vendor = getVendor(detail.agent);
  const status = STATUSES[getStatus(detail.status)];
  const context =
    detail.contextTokens !== null && detail.contextWindow !== null && detail.contextWindow > 0
      ? Math.round((detail.contextTokens / detail.contextWindow) * 100)
      : null;
  return (
    <div className="session-canvas-stack">
      <div className="text-muted-foreground truncate font-mono text-[10px]" title={detail.id}>
        {detail.agent}:{detail.id}
      </div>
      <SessionNameEditor
        name={detail.name}
        fallback={detail.repo ?? 'Untitled session'}
        onRename={onRename}
      />
      <div className="text-muted-foreground truncate text-[11px]">
        {detail.repo ?? 'unknown repo'} / {detail.branch ?? 'no branch'}
      </div>
      <div className="flex flex-wrap gap-1.5">
        <Badge className={cn('text-[10px]', vendor.fg, vendor.bg)}>{vendor.label}</Badge>
        <Metric>{detail.turns} turns</Metric>
        <Metric>{detail.toolUses} tools</Metric>
        <Metric>{detail.fileEdits.length} files</Metric>
      </div>
      <div className="session-canvas-stat-grid">
        <div>
          <span>Context</span>
          <strong>{context === null ? '—' : `${context}%`}</strong>
        </div>
        <div>
          <span>Tokens</span>
          <strong>{getTotalTokens(detail.tokens).toLocaleString()}</strong>
        </div>
      </div>
      <div className={cn('flex items-center gap-1 text-[10px] font-semibold', status.fg)}>
        <span className={cn('size-1.5 rounded-full', status.dot)} />
        {status.label}
      </div>
    </div>
  );
}

function GoalView({ detail }: { detail: SessionCanvasDetail }) {
  return (
    <div className="session-canvas-stack">
      <blockquote>{detail.firstPrompt ?? 'No opening prompt was captured.'}</blockquote>
      <div className="session-canvas-callout">
        <strong>Latest outcome</strong>
        <p>{detail.synthesis?.outcome || detail.summary || 'No outcome has been summarized yet.'}</p>
      </div>
    </div>
  );
}

function PlanView({ detail }: { detail: SessionCanvasDetail }) {
  const todos =
    detail.todos.length > 0 ? detail.todos : detail.turnLog.flatMap((turn) => turn.todos).slice(-6);
  if (todos.length === 0) return <Empty>No plan items were captured.</Empty>;
  return (
    <ol className="session-canvas-list">
      {todos.map((todo, index) => (
        <li key={`${index}:${todo.text}`} className={cn(todo.done && 'session-canvas-complete')}>
          {todo.done ? <CheckCircle2Icon /> : <span className="session-canvas-list-dot" />}
          <span>{todo.text}</span>
        </li>
      ))}
    </ol>
  );
}

function TimelineView({ detail }: { detail: SessionCanvasDetail }) {
  if (detail.turnLog.length === 0) return <Empty>No turns were captured.</Empty>;
  return (
    <div className="session-canvas-timeline">
      {detail.turnLog.slice(-8).map((turn) => (
        <div key={turn.index}>
          <span className={cn('session-canvas-turn-dot', turn.errors > 0 && 'session-canvas-turn-error')} />
          <strong>Turn {turn.index}</strong>
          <small>
            {turn.toolUses} tools · {turn.fileEdits.length} files
          </small>
        </div>
      ))}
    </div>
  );
}

function ContextView({
  detail,
  onOpenFile,
}: {
  detail: SessionCanvasDetail;
  onOpenFile: (path: string) => void;
}) {
  if (detail.contextFiles.length === 0 && detail.triggeredContext.length === 0)
    return <Empty>No captured context.</Empty>;
  return (
    <div className="canvas-node-scroll session-canvas-stack">
      {detail.contextFiles.map((file) => (
        <button
          type="button"
          className="session-canvas-row"
          key={file.path}
          onClick={(event) => {
            event.stopPropagation();
            onOpenFile(file.path);
          }}
        >
          <FilesIcon />
          <span title={file.path}>{file.path}</span>
          {file.partial && <Badge variant="secondary">partial</Badge>}
        </button>
      ))}
      {detail.triggeredContext.map((item) => (
        <div className="session-canvas-row" key={`${item.kind}:${item.name}`}>
          <BracesIcon />
          <span>{item.name}</span>
          <small>{item.calls}×</small>
        </div>
      ))}
    </div>
  );
}

function ChangesView({
  detail,
  onOpenFile,
}: {
  detail: SessionCanvasDetail;
  onOpenFile: (path: string) => void;
}) {
  if (detail.fileEdits.length === 0) return <Empty>No worktree changes.</Empty>;
  return (
    <div className="canvas-node-scroll session-canvas-stack">
      {detail.fileEdits.map((file) => (
        <button
          type="button"
          className="session-canvas-row"
          key={file.path}
          onClick={(event) => {
            event.stopPropagation();
            onOpenFile(file.path);
          }}
        >
          <FileDiffIcon />
          <span title={file.path}>{file.path}</span>
          <small className="text-success-fg whitespace-nowrap">+{file.adds}</small>
          <small className="text-destructive whitespace-nowrap">−{file.dels}</small>
        </button>
      ))}
    </div>
  );
}

function TurnView({
  detail,
  analyses,
  disabled,
  onAnalyze,
}: {
  detail: SessionCanvasDetail;
  analyses: ReadonlyMap<number, TurnAnalysis | 'loading' | 'disabled' | 'failed'>;
  disabled: boolean;
  onAnalyze: (turn: number) => void;
}) {
  if (detail.turnLog.length === 0) return <Empty>No turn details are available.</Empty>;
  return (
    <div className="canvas-node-scroll session-canvas-stack">
      {detail.turnLog.map((turn) => {
        const analysis = analyses.get(turn.index);
        return (
          <section className="session-canvas-turn-card" key={turn.index}>
            <div className="flex items-center justify-between gap-2">
              <strong>Turn {turn.index}</strong>
              <Button
                variant="ghost"
                size="xs"
                disabled={disabled || analysis === 'loading'}
                onClick={() => onAnalyze(turn.index)}
              >
                <SparklesIcon /> Analyze
              </Button>
            </div>
            <p>{turn.prompt || 'No user prompt captured.'}</p>
            {analysis !== undefined && typeof analysis !== 'string' && (
              <div className="session-canvas-analysis">
                <strong>{analysis.status}</strong>
                <span>{analysis.intention}</span>
                <small>{analysis.planSummary}</small>
              </div>
            )}
            {analysis === 'disabled' && <small>AI analysis is disabled.</small>}
            {analysis === 'failed' && <small role="alert">Analysis failed safely.</small>}
          </section>
        );
      })}
    </div>
  );
}

function TerminalView({
  terminal,
  onLaunch,
  onInput,
  onResize,
  onReconnect,
  onStop,
}: {
  terminal: TerminalConnectionSnapshot | null;
  onLaunch: () => void;
  onInput: (data: string) => void;
  onResize: (cols: number, rows: number) => void;
  onReconnect: () => void;
  onStop: () => void;
}) {
  const [draft, setDraft] = useState('');
  const outputRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    const output = outputRef.current;
    if (output !== null) output.scrollTop = output.scrollHeight;
  }, [terminal?.output]);

  useEffect(() => {
    const output = outputRef.current;
    if (terminal?.status !== 'open' || output === null || typeof ResizeObserver === 'undefined') return;
    let last = '';
    const publish = (width: number, height: number) => {
      const cols = Math.max(20, Math.min(500, Math.floor(width / 7.2)));
      const rows = Math.max(5, Math.min(200, Math.floor(height / 16)));
      const dimensions = `${cols}:${rows}`;
      if (dimensions === last) return;
      last = dimensions;
      onResize(cols, rows);
    };
    const observer = new ResizeObserver((entries) => {
      const box = entries[0]?.contentRect;
      if (box !== undefined) publish(box.width, box.height);
    });
    observer.observe(output);
    const box = output.getBoundingClientRect();
    publish(box.width, box.height);
    return () => observer.disconnect();
  }, [onResize, terminal?.status]);

  if (terminal === null)
    return (
      <div className="session-canvas-terminal-empty">
        <TerminalIcon />
        <p>Attach a guarded native terminal to this exact session.</p>
        <Button size="sm" onClick={onLaunch}>
          <PlayIcon /> Open terminal
        </Button>
      </div>
    );
  return (
    <div className="session-canvas-terminal">
      <div className="session-canvas-terminal-status">
        <span
          className={cn('size-1.5 rounded-full', terminal.status === 'open' ? 'bg-success' : 'bg-warning')}
        />
        {terminal.status}
        {terminal.attempts > 0 && <span>· reconnect {terminal.attempts}</span>}
        <span className="session-canvas-terminal-actions">
          {(terminal.status === 'error' || terminal.status === 'closed') && (
            <Button variant="ghost" size="xs" onClick={onReconnect}>
              <RefreshCwIcon /> Retry connection
            </Button>
          )}
          <Button variant="ghost" size="xs" onClick={onStop}>
            <XIcon /> Stop
          </Button>
        </span>
      </div>
      <pre
        ref={outputRef}
        role="log"
        tabIndex={terminal.status === 'open' ? 0 : -1}
        aria-label="Interactive terminal output"
        onKeyDown={(event) => {
          if (terminal.status !== 'open') return;
          const data = terminalKeyData(event);
          if (data === null) return;
          event.preventDefault();
          event.stopPropagation();
          onInput(data);
        }}
        onPaste={(event) => {
          if (terminal.status !== 'open') return;
          const data = event.clipboardData.getData('text');
          if (data === '') return;
          event.preventDefault();
          onInput(bracketedPaste(data));
        }}
      >
        {terminal.output || 'Connected. Focus this terminal to type, or use the command field below.'}
      </pre>
      <form
        className="session-canvas-terminal-input"
        onSubmit={(event) => {
          event.preventDefault();
          if (draft === '' || terminal.status !== 'open') return;
          onInput(`${draft}\r`);
          setDraft('');
        }}
      >
        <input
          value={draft}
          disabled={terminal.status !== 'open'}
          maxLength={32_000}
          onChange={(event) => setDraft(event.target.value)}
          placeholder={terminal.status === 'open' ? 'Type a terminal command…' : 'Terminal reconnecting…'}
          aria-label="Terminal input"
        />
        <Button type="submit" size="xs" disabled={terminal.status !== 'open' || draft === ''}>
          <SendIcon /> Send
        </Button>
      </form>
    </div>
  );
}

const NODE_META: Record<SessionNodeId, { title: string; icon: ComponentType<{ className?: string }> }> = {
  session: { title: 'SESSION', icon: BotIcon },
  goal: { title: 'GOAL & OUTCOME', icon: LightbulbIcon },
  plan: { title: 'PLAN', icon: ClipboardListIcon },
  timeline: { title: 'TIMELINE', icon: MapIcon },
  context: { title: 'CONTEXT', icon: FilesIcon },
  changes: { title: 'WORKTREE', icon: FileDiffIcon },
  terminal: { title: 'NEXT MOVE · LIVE TERMINAL', icon: TerminalIcon },
  note: { title: 'MY NOTE', icon: StickyNoteIcon },
  turn: { title: 'TURN INSPECTOR', icon: MessagesSquareIcon },
};

export type SessionCanvasWorkbenchProps = {
  detail: SessionCanvasDetail;
  workspace: SessionCanvasWorkspace;
  persistence: Pick<WorkspaceSnapshot<SessionCanvasWorkspace>, 'status' | 'dirty' | 'error'>;
  terminal: TerminalConnectionSnapshot | null;
  analyses: ReadonlyMap<number, TurnAnalysis | 'loading' | 'disabled' | 'failed'>;
  actionError?: string;
  aiDisabled?: boolean;
  onWorkspaceChange: (workspace: SessionCanvasWorkspace) => void;
  onRename: (name: string) => void;
  onLaunchTerminal: () => void;
  onTerminalInput: (data: string) => void;
  onTerminalResize: (cols: number, rows: number) => void;
  onReconnectTerminal: () => void;
  onStopTerminal: () => void;
  onSendNote: () => void;
  onResolvePersistence: () => void;
  onReloadWorkspace: () => void;
  onLoadFile: (path: string) => Promise<SessionFilePreview>;
  onAnalyze: (turn: number) => void;
  onFork: (checkpointId: string, prompt: string) => void;
  onPromote: (session: CanvasSessionIdentity, checkpointId: string, experimentId: string) => void;
};

export function SessionCanvasWorkbench({
  detail,
  workspace,
  persistence,
  terminal,
  analyses,
  actionError = '',
  aiDisabled = false,
  onWorkspaceChange,
  onRename,
  onLaunchTerminal,
  onTerminalInput,
  onTerminalResize,
  onReconnectTerminal,
  onStopTerminal,
  onSendNote,
  onResolvePersistence,
  onReloadWorkspace,
  onLoadFile,
  onAnalyze,
  onFork,
  onPromote,
}: SessionCanvasWorkbenchProps) {
  const [selected, setSelected] = useState<SessionNodeId | null>(null);
  const [focused, setFocused] = useState<SessionNodeId | null>(null);
  const [zoom, setZoom] = useState(0.8);
  const [panel, setPanel] = useState<Panel>(null);
  const [commandOpen, setCommandOpen] = useState(false);
  const [experimentPrompt, setExperimentPrompt] = useState('');
  const [filePreview, setFilePreview] = useState<
    | { path: string; state: 'loading'; content: ''; contentType: '' }
    | ({ path: string; state: 'ready' } & SessionFilePreview)
    | { path: string; state: 'error'; content: string; contentType: '' }
    | null
  >(null);
  const fileRequest = useRef(0);
  const attention = useMemo(() => sessionAttention(detail), [detail]);
  const pins = useMemo(() => sessionPinCandidates(detail), [detail]);
  const stalePinIds = useMemo(() => staleSessionPinIds(workspace.pinIds, pins), [pins, workspace.pinIds]);
  const selectedCheckpoint = workspace.checkpoints.at(-1) ?? null;

  const update = useCallback(
    (action: Parameters<typeof reduceSessionWorkspace>[1]) =>
      onWorkspaceChange(reduceSessionWorkspace(workspace, action)),
    [onWorkspaceChange, workspace],
  );
  const interaction = useCanvasNodeInteraction<SessionNodeId>({
    zoom,
    disabled: focused !== null,
    world: SESSION_CANVAS_WORLD,
    minWidth: SESSION_NODE_MIN_WIDTH,
    minHeight: SESSION_NODE_MIN_HEIGHT,
    getLayout: (id) => workspace.layout[id],
    updateLayouts: (updates) => onWorkspaceChange(reduceSessionLayoutUpdates(workspace, updates)),
    onSelect: setSelected,
    getCompanions: (id) => (id === 'terminal' ? ['note'] : []),
  });

  const onBoardKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (isTextEntryTarget(event.target as HTMLElement)) return;
    const command = boardCommandFor(event);
    if (command === null) return;
    event.preventDefault();
    if (command === 'exit-focus') {
      setFocused(null);
      setPanel(null);
      setSelected(null);
      fileRequest.current += 1;
      setFilePreview(null);
    }
    if (command === 'open-command-palette') setCommandOpen(true);
    if (command === 'zoom-in') setZoom((value) => Math.min(1.25, value + 0.1));
    if (command === 'zoom-out') setZoom((value) => Math.max(0.4, value - 0.1));
    if (command === 'zoom-reset') setZoom(1);
  };

  const nodeProps = (id: SessionNodeId) => ({
    id,
    title: NODE_META[id].title,
    icon: NODE_META[id].icon,
    layout: workspace.layout[id],
    selected: selected === id,
    focused: focused === id,
    focusActive: focused !== null,
    onSelect: setSelected,
    onFocus: (node: SessionNodeId) => setFocused((current) => (current === node ? null : node)),
    onToggleCollapse: (node: SessionNodeId) => update({ type: 'toggle-collapse', id: node }),
    onToggleLock: (node: SessionNodeId) => update({ type: 'toggle-lock', id: node }),
    onDragStart: interaction.onDragStart,
    onResizeStart: interaction.onResizeStart,
  });

  const createNewCheckpoint = () => {
    const checkpoint = createCheckpoint(detail, crypto.randomUUID(), Date.now());
    update({ type: 'add-checkpoint', checkpoint });
    showPanel('checkpoints');
  };

  const showPanel = (next: Exclude<Panel, null>) => {
    fileRequest.current += 1;
    setFilePreview(null);
    setPanel(next);
  };

  const exportWorkspace = () => {
    const blob = new Blob(
      [JSON.stringify({ session: { agent: detail.agent, id: detail.id }, workspace }, null, 2)],
      {
        type: 'application/json',
      },
    );
    const link = document.createElement('a');
    link.href = URL.createObjectURL(blob);
    link.download = `canvas-${detail.agent}-${detail.id}.json`;
    link.click();
    URL.revokeObjectURL(link.href);
  };

  const openFile = async (path: string) => {
    const request = ++fileRequest.current;
    setPanel(null);
    setFilePreview({ path, state: 'loading', content: '', contentType: '' });
    try {
      const preview = await onLoadFile(path);
      if (fileRequest.current === request) setFilePreview({ path, state: 'ready', ...preview });
    } catch (caught) {
      if (fileRequest.current !== request) return;
      setFilePreview({
        path,
        state: 'error',
        content: caught instanceof Error ? caught.message : 'File preview is unavailable.',
        contentType: '',
      });
    }
  };

  const wires: [SessionNodeId, SessionNodeId][] = [
    ['session', 'goal'],
    ['goal', 'plan'],
    ['plan', 'timeline'],
    ['timeline', 'context'],
    ['context', 'changes'],
    ['changes', 'terminal'],
    ['terminal', 'note'],
    ['timeline', 'turn'],
  ];

  return (
    <div
      className="session-canvas"
      data-session-key={`${detail.agent}:${detail.id}`}
      onKeyDown={onBoardKeyDown}
    >
      <CanvasStage
        toolbar={
          <div className="flex w-full items-center justify-between gap-2">
            <div className="canvas-tool-group">
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => showPanel('attention')}
                aria-label="Attention"
              >
                <BellIcon />
                {attention.length > 0 && <span className="canvas-tool-count">{attention.length}</span>}
              </Button>
              <Button variant="ghost" size="icon-sm" onClick={() => showPanel('pins')} aria-label="Pins">
                <PinIcon />
                {workspace.pinIds.length > 0 && (
                  <span className="canvas-tool-count">{workspace.pinIds.length}</span>
                )}
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => showPanel('checkpoints')}
                aria-label="Checkpoints"
              >
                <GitForkIcon />
                {workspace.checkpoints.length > 0 && (
                  <span className="canvas-tool-count">{workspace.checkpoints.length}</span>
                )}
              </Button>
            </div>
            <div className="canvas-tool-group">
              <span className={cn('session-canvas-save-state', persistence.dirty && 'text-warning-fg')}>
                {persistence.status === 'saving' ? 'Saving…' : persistence.dirty ? 'Unsaved' : 'Saved'}
              </span>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={createNewCheckpoint}
                aria-label="Create checkpoint"
              >
                <PlusIcon />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() =>
                  onWorkspaceChange({ ...workspace, layout: autoArrangeSessionLayout(workspace.layout) })
                }
                aria-label="Auto arrange"
              >
                <ListRestartIcon />
              </Button>
              <Button variant="ghost" size="icon-sm" onClick={exportWorkspace} aria-label="Export Canvas">
                <DownloadIcon />
              </Button>
            </div>
          </div>
        }
      >
        <CanvasWorldLayer world={SESSION_CANVAS_WORLD} zoom={zoom} hasFocus={focused !== null}>
          <CanvasWires world={SESSION_CANVAS_WORLD}>
            {wires.map(([from, to]) => (
              <path key={`${from}-${to}`} d={triggerWirePath(workspace.layout[from], workspace.layout[to])} />
            ))}
          </CanvasWires>
          <CanvasNode {...nodeProps('session')} meta={<Badge variant="secondary">{detail.agent}</Badge>}>
            <SessionOverview detail={detail} onRename={onRename} />
          </CanvasNode>
          <CanvasNode {...nodeProps('goal')}>
            <GoalView detail={detail} />
          </CanvasNode>
          <CanvasNode {...nodeProps('plan')} meta={<Badge variant="secondary">{detail.todos.length}</Badge>}>
            <PlanView detail={detail} />
          </CanvasNode>
          <CanvasNode
            {...nodeProps('timeline')}
            meta={<Badge variant="secondary">{detail.turnLog.length}</Badge>}
          >
            <TimelineView detail={detail} />
          </CanvasNode>
          <CanvasNode
            {...nodeProps('context')}
            meta={<Badge variant="secondary">{detail.contextFiles.length}</Badge>}
          >
            <ContextView detail={detail} onOpenFile={openFile} />
          </CanvasNode>
          <CanvasNode
            {...nodeProps('changes')}
            meta={<Badge variant="secondary">{detail.fileEdits.length}</Badge>}
          >
            <ChangesView detail={detail} onOpenFile={openFile} />
          </CanvasNode>
          <CanvasNode {...nodeProps('terminal')} className="session-canvas-terminal-node">
            <TerminalView
              terminal={terminal}
              onLaunch={onLaunchTerminal}
              onInput={onTerminalInput}
              onResize={onTerminalResize}
              onReconnect={onReconnectTerminal}
              onStop={onStopTerminal}
            />
          </CanvasNode>
          <CanvasNode {...nodeProps('note')} className="session-canvas-note-node">
            <div className="session-canvas-note">
              <textarea
                value={workspace.note}
                maxLength={8_000}
                placeholder="Write a note to paste into the live terminal…"
                onChange={(event) => update({ type: 'note', value: event.target.value })}
              />
              <Button
                size="sm"
                disabled={terminal?.status !== 'open' || workspace.note.trim() === ''}
                onClick={onSendNote}
              >
                <SendIcon /> Send to terminal
              </Button>
            </div>
          </CanvasNode>
          <CanvasNode
            {...nodeProps('turn')}
            meta={<Badge variant="secondary">{detail.turnLog.length}</Badge>}
          >
            <TurnView detail={detail} analyses={analyses} disabled={aiDisabled} onAnalyze={onAnalyze} />
          </CanvasNode>
        </CanvasWorldLayer>
        <ZoomControls
          zoom={zoom}
          bounds={{ ...CANVAS_DEFAULT_ZOOM_BOUNDS, min: 0.4, max: 1.25, step: 0.1 }}
          onChange={setZoom}
          onReset={() => setZoom(1)}
        />
      </CanvasStage>

      {(persistence.error !== null || actionError !== '') && (
        <div className="session-canvas-persistence-error" role="alert">
          {[persistence.error?.message, actionError]
            .filter((message): message is string => message !== undefined && message !== '')
            .filter((message, index, messages) => messages.indexOf(message) === index)
            .map((message) => (
              <div key={message}>{message}</div>
            ))}
          {persistence.error?.isConflict && (
            <div className="session-canvas-persistence-actions">
              <Button size="xs" onClick={onResolvePersistence}>
                Keep local
              </Button>
              <Button size="xs" variant="outline" onClick={onReloadWorkspace}>
                Reload server
              </Button>
            </div>
          )}
        </div>
      )}
      {panel === 'attention' && (
        <CanvasSidePanel title="Attention" onClose={() => setPanel(null)}>
          {attention.length === 0 ? (
            <Empty>Nothing needs attention.</Empty>
          ) : (
            attention.map((item) => (
              <button
                className="session-canvas-panel-row"
                key={item.id}
                onClick={() => {
                  setSelected(item.node);
                  setPanel(null);
                }}
              >
                <AlertTriangleIcon
                  className={item.tone === 'error' ? 'text-destructive' : 'text-warning-fg'}
                />
                <span>
                  <strong>{item.title}</strong>
                  <small>{item.detail}</small>
                </span>
              </button>
            ))
          )}
        </CanvasSidePanel>
      )}
      {filePreview !== null && (
        <CanvasSidePanel
          title={filePreview.path}
          onClose={() => {
            fileRequest.current += 1;
            setFilePreview(null);
          }}
        >
          {filePreview.state === 'loading' ? (
            <Empty>Loading guarded file preview…</Empty>
          ) : filePreview.state === 'error' ? (
            <p role="alert" className="text-destructive text-xs">
              {filePreview.content}
            </p>
          ) : (
            <div className="session-canvas-file-preview">
              <small>{filePreview.contentType}</small>
              <pre>{filePreview.content}</pre>
            </div>
          )}
        </CanvasSidePanel>
      )}
      {panel === 'pins' && (
        <CanvasSidePanel title="Pins" onClose={() => setPanel(null)}>
          {pins.map((pin) => (
            <label className="session-canvas-panel-row" key={pin.id}>
              <input
                type="checkbox"
                checked={workspace.pinIds.includes(pin.id)}
                onChange={() => update({ type: 'toggle-pin', id: pin.id })}
              />
              <span>
                <strong>
                  {pin.kind}: {pin.label}
                </strong>
                <small>{pin.detail}</small>
              </span>
            </label>
          ))}
          {stalePinIds.map((id) => (
            <label className="session-canvas-panel-row" key={id}>
              <input type="checkbox" checked onChange={() => update({ type: 'toggle-pin', id })} />
              <span>
                <strong>Unavailable pin</strong>
                <small>{id}</small>
              </span>
            </label>
          ))}
        </CanvasSidePanel>
      )}
      {panel === 'checkpoints' && (
        <CanvasSidePanel title="Checkpoints & experiments" onClose={() => setPanel(null)}>
          <Button size="sm" onClick={createNewCheckpoint}>
            <PlusIcon /> Create checkpoint
          </Button>
          {workspace.checkpoints.map((checkpoint) => (
            <CheckpointView
              key={checkpoint.id}
              checkpoint={checkpoint}
              onCompare={() => showPanel('compare')}
            />
          ))}
          {selectedCheckpoint !== null && (
            <div className="session-canvas-experiment-form">
              <textarea
                value={experimentPrompt}
                onChange={(event) => setExperimentPrompt(event.target.value)}
                placeholder="Experiment prompt"
                maxLength={2_000}
              />
              <Button
                size="sm"
                disabled={experimentPrompt.trim() === ''}
                onClick={() => {
                  onFork(selectedCheckpoint.id, experimentPrompt.trim());
                  setExperimentPrompt('');
                }}
              >
                <GitForkIcon /> Fork experiment
              </Button>
            </div>
          )}
        </CanvasSidePanel>
      )}
      {panel === 'compare' && (
        <ComparisonPanel
          checkpoints={workspace.checkpoints}
          onClose={() => setPanel(null)}
          onPromote={onPromote}
        />
      )}
      {selected !== null && focused === null && (
        <CanvasInspector title={NODE_META[selected].title} onClose={() => setSelected(null)}>
          <p className="text-muted-foreground text-xs">
            Selected {NODE_META[selected].title.toLowerCase()} node. Double-click it to focus; press C or L on
            its chrome to collapse or lock.
          </p>
        </CanvasInspector>
      )}
      <CanvasCommandOverlay open={commandOpen} onClose={() => setCommandOpen(false)}>
        <div className="p-3">
          <strong>Canvas commands</strong>
          <div className="session-canvas-command-list">
            <button onClick={createNewCheckpoint}>Create checkpoint</button>
            <button onClick={() => showPanel('pins')}>Open pins</button>
            <button onClick={() => showPanel('attention')}>Review attention</button>
            <button
              onClick={() =>
                onWorkspaceChange({ ...workspace, layout: autoArrangeSessionLayout(workspace.layout) })
              }
            >
              Auto arrange
            </button>
          </div>
        </div>
      </CanvasCommandOverlay>
    </div>
  );
}

function CheckpointView({ checkpoint, onCompare }: { checkpoint: SessionCheckpoint; onCompare: () => void }) {
  return (
    <section className="session-canvas-checkpoint">
      <div className="flex items-center justify-between">
        <strong>{checkpoint.name}</strong>
        <Button variant="ghost" size="xs" onClick={onCompare}>
          Compare
        </Button>
      </div>
      <small>
        {checkpoint.snapshot.modifiedFiles} files · {checkpoint.snapshot.openTasks} open tasks
      </small>
      <div>
        {checkpoint.experiments.map((experiment) => (
          <Badge key={experiment.id} variant={experiment.status === 'failed' ? 'destructive' : 'secondary'}>
            {experiment.status}
          </Badge>
        ))}
      </div>
    </section>
  );
}

function ComparisonPanel({
  checkpoints,
  onClose,
  onPromote,
}: {
  checkpoints: SessionCheckpoint[];
  onClose: () => void;
  onPromote: (session: CanvasSessionIdentity, checkpointId: string, experimentId: string) => void;
}) {
  const experiments = checkpoints.flatMap((checkpoint) =>
    checkpoint.experiments.map((experiment) => ({ checkpoint, experiment })),
  );
  return (
    <div
      className="session-canvas-comparison"
      role="dialog"
      aria-modal="true"
      aria-label="Experiment comparison"
    >
      <header>
        <strong>Experiment comparison</strong>
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close comparison">
          <XIcon />
        </Button>
      </header>
      <div className="session-canvas-comparison-grid">
        {experiments.length === 0 ? (
          <Empty>No experiments to compare.</Empty>
        ) : (
          experiments.map(({ checkpoint, experiment }) => (
            <article key={experiment.id}>
              <Badge variant="secondary">{experiment.status}</Badge>
              <strong>{experiment.prompt}</strong>
              <small>{checkpoint.name}</small>
              {experiment.error && <p role="alert">{experiment.error}</p>}
              {experiment.childSession && (
                <Button
                  size="sm"
                  disabled={experiment.promotedAt !== undefined}
                  onClick={() => onPromote(experiment.childSession!, checkpoint.id, experiment.id)}
                >
                  {experiment.promotedAt ? 'Promoted' : 'Promote session'}
                </Button>
              )}
            </article>
          ))
        )}
      </div>
    </div>
  );
}

export type SessionCanvasProps = {
  session: CanvasSessionIdentity | null;
  freshnessVersion: number;
  fetch?: SessionFetch;
  aiDisabled?: boolean;
  onInspectSession?: (session: CanvasSessionIdentity) => void;
};

export function SessionCanvas({
  session,
  freshnessVersion,
  fetch,
  aiDisabled,
  onInspectSession,
}: SessionCanvasProps) {
  const sessionAgent = session?.agent ?? null;
  const sessionId = session?.id ?? null;
  const identity = useMemo<CanvasSessionIdentity | null>(
    () => (sessionAgent === null || sessionId === null ? null : { agent: sessionAgent, id: sessionId }),
    [sessionAgent, sessionId],
  );
  const [detail, setDetail] = useState<SessionCanvasDetail | null>(null);
  const [workspace, setWorkspace] = useState(defaultSessionWorkspace);
  const [workspaceFormatError, setWorkspaceFormatError] = useState('');
  const [loadState, setLoadState] = useState<'idle' | 'loading' | 'error'>('idle');
  const [error, setError] = useState('');
  const [persistence, setPersistence] = useState<WorkspaceSnapshot<SessionCanvasWorkspace>>({
    state: null,
    revision: 0,
    status: 'idle',
    dirty: false,
    loaded: false,
    error: null,
  });
  const [terminalId, setTerminalId] = useState<string | null>(null);
  const [terminalEpoch, setTerminalEpoch] = useState(0);
  const [terminal, setTerminal] = useState<TerminalConnectionSnapshot | null>(null);
  const [analyses, setAnalyses] = useState<Map<number, TurnAnalysis | 'loading' | 'disabled' | 'failed'>>(
    new Map(),
  );
  const workspaceClient = useMemo(
    () =>
      identity === null ? null : createWorkspaceClient<SessionCanvasWorkspace>({ session: identity, fetch }),
    [fetch, identity],
  );
  const terminalRef = useRef<ReturnType<typeof createTerminalConnection> | null>(null);
  const workspaceRef = useRef(workspace);
  workspaceRef.current = workspace;
  const workspaceWritableRef = useRef(false);
  const currentIdentityKey = identity === null ? '' : sessionKey(identity);
  const activeIdentityKeyRef = useRef(currentIdentityKey);
  activeIdentityKeyRef.current = currentIdentityKey;

  useEffect(() => {
    const initialWorkspace = defaultSessionWorkspace();
    workspaceWritableRef.current = false;
    workspaceRef.current = initialWorkspace;
    setWorkspace(initialWorkspace);
    setWorkspaceFormatError('');
    setPersistence({
      state: null,
      revision: 0,
      status: 'idle',
      dirty: false,
      loaded: false,
      error: null,
    });
    setTerminalId(null);
    setTerminalEpoch(0);
    setAnalyses(new Map());
  }, [identity]);

  useEffect(() => {
    if (identity === null) {
      setDetail(null);
      setLoadState('idle');
      return;
    }
    let current = true;
    setLoadState('loading');
    loadSessionDetail(identity, fetch)
      .then((next) => {
        if (current) {
          setDetail(next);
          setError('');
          setLoadState('idle');
        }
      })
      .catch((caught: unknown) => {
        if (current) {
          setError(caught instanceof Error ? caught.message : 'Session details are unavailable.');
          setLoadState('error');
        }
      });
    return () => {
      current = false;
    };
  }, [fetch, freshnessVersion, identity]);

  useEffect(() => {
    if (workspaceClient === null) return;
    let adopted = false;
    const publish = () => {
      const snapshot = workspaceClient.snapshot();
      setPersistence(snapshot);
      if (!adopted && snapshot.loaded) {
        adopted = true;
        try {
          const restored =
            snapshot.state === null ? defaultSessionWorkspace() : normalizeSessionWorkspace(snapshot.state);
          workspaceWritableRef.current = true;
          workspaceRef.current = restored;
          setWorkspace(restored);
          setWorkspaceFormatError('');
        } catch (caught) {
          workspaceWritableRef.current = false;
          setWorkspaceFormatError(
            caught instanceof Error ? caught.message : 'This Canvas workspace format is unsupported.',
          );
        }
      }
    };
    const unsubscribe = workspaceClient.subscribe(publish);
    void workspaceClient.load();
    publish();
    return () => {
      unsubscribe();
      void workspaceClient.flush().finally(() => workspaceClient.dispose());
    };
  }, [workspaceClient]);

  useEffect(() => {
    terminalRef.current?.close();
    terminalRef.current = null;
    if (terminalId === null) {
      setTerminal(null);
      return;
    }
    const connection = createTerminalConnection({ terminalId });
    terminalRef.current = connection;
    const publish = () => setTerminal(connection.snapshot());
    const unsubscribe = connection.subscribe(publish);
    publish();
    return () => {
      unsubscribe();
      connection.close();
    };
  }, [terminalEpoch, terminalId]);

  if (identity === null)
    return (
      <div className="session-canvas-state">
        <MessageSquareTextIcon />
        <strong>Select a session for Canvas</strong>
        <p>Canvas keeps Claude and Codex sessions distinct by agent and id.</p>
      </div>
    );
  if (detail === null || detail.agent !== identity.agent || detail.id !== identity.id)
    return (
      <div className="session-canvas-state" role={loadState === 'error' ? 'alert' : undefined}>
        {loadState === 'loading' ? <LoaderCircleIcon className="animate-spin" /> : <AlertTriangleIcon />}
        <strong>
          {loadState === 'loading' ? 'Building session workbench…' : 'Canvas session unavailable'}
        </strong>
        <p>{loadState === 'error' ? error : 'Loading the on-demand session projection.'}</p>
        {loadState === 'error' && (
          <Button size="sm" onClick={() => onInspectSession?.(identity)}>
            <RefreshCwIcon /> Inspect session
          </Button>
        )}
      </div>
    );
  if (workspaceFormatError !== '')
    return (
      <div className="session-canvas-state" role="alert">
        <AlertTriangleIcon />
        <strong>Canvas workspace version unsupported</strong>
        <p>{workspaceFormatError} It remains unchanged on the server.</p>
      </div>
    );
  if (!persistence.loaded)
    return (
      <div className="session-canvas-state" role={persistence.error === null ? undefined : 'alert'}>
        {persistence.error === null ? <LoaderCircleIcon className="animate-spin" /> : <AlertTriangleIcon />}
        <strong>
          {persistence.error === null ? 'Restoring Canvas workspace…' : 'Canvas workspace unavailable'}
        </strong>
        <p>{persistence.error?.message ?? 'Loading the server-backed layout and workflow state.'}</p>
        {persistence.error !== null && (
          <Button size="sm" onClick={() => void workspaceClient?.load()}>
            <RefreshCwIcon /> Retry workspace
          </Button>
        )}
      </div>
    );

  const commitWorkspace = (next: SessionCanvasWorkspace) => {
    if (!workspaceWritableRef.current) return false;
    workspaceRef.current = next;
    setWorkspace(next);
    workspaceClient?.update(next);
    return true;
  };
  const applyWorkspaceAction = (
    action: Parameters<typeof reduceSessionWorkspace>[1],
    expectedIdentityKey = currentIdentityKey,
  ) => {
    const next = reduceWorkspaceForIdentity(
      activeIdentityKeyRef.current,
      expectedIdentityKey,
      workspaceRef.current,
      action,
    );
    if (next === null) return false;
    return commitWorkspace(next);
  };
  const launchTerminal = async () => {
    const actionIdentityKey = currentIdentityKey;
    try {
      const response = await launchSessionTerminal(identity, {}, fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setTerminalId(response.terminal.terminalId);
      setError('');
    } catch (caught) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setError(caught instanceof Error ? caught.message : 'Terminal failed to start.');
    }
  };
  const rename = async (name: string) => {
    const actionIdentityKey = currentIdentityKey;
    try {
      await renameSession(identity, name, fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setDetail((current) =>
        current === null || current.agent !== identity.agent || current.id !== identity.id
          ? current
          : { ...current, name },
      );
      setError('');
    } catch (caught) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setError(caught instanceof Error ? caught.message : 'Rename failed.');
    }
  };
  const analyze = async (turn: number) => {
    const actionIdentityKey = currentIdentityKey;
    setAnalyses((current) => new Map(current).set(turn, 'loading'));
    try {
      const response = await analyzeTurn(identity, turn, fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setAnalyses((current) => new Map(current).set(turn, response.analysis));
    } catch (caught: unknown) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      const code =
        typeof caught === 'object' && caught !== null && 'code' in caught ? String(caught.code) : '';
      setAnalyses((current) =>
        new Map(current).set(turn, code === 'ANALYSIS_DISABLED' ? 'disabled' : 'failed'),
      );
    }
  };
  const sendNote = async () => {
    if (terminalId === null || workspace.note.trim() === '') return;
    const actionIdentityKey = currentIdentityKey;
    try {
      await sendTerminalInput(terminalId, bracketedPaste(workspace.note), fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setError('');
    } catch (caught) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setError(caught instanceof Error ? caught.message : 'Note delivery failed.');
    }
  };
  const fork = async (checkpointId: string, prompt: string) => {
    const actionIdentityKey = currentIdentityKey;
    const id = crypto.randomUUID();
    const experiment: SessionExperiment = { id, prompt, createdAt: Date.now(), status: 'launching' };
    applyWorkspaceAction({ type: 'add-experiment', checkpointId, experiment }, actionIdentityKey);
    try {
      const response = await forkSession(identity, { prompt, writable: true }, fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      applyWorkspaceAction({
        type: 'finish-experiment',
        checkpointId,
        experimentId: id,
        patch: {
          status: 'launched',
          terminalId: response.terminal.terminalId,
          childSession: response.childSessionId
            ? { agent: identity.agent, id: response.childSessionId }
            : undefined,
        },
      });
      setTerminalId(response.terminal.terminalId);
    } catch (caught) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      applyWorkspaceAction({
        type: 'finish-experiment',
        checkpointId,
        experimentId: id,
        patch: { status: 'failed', error: caught instanceof Error ? caught.message : 'Experiment failed.' },
      });
    }
  };
  const promote = (child: CanvasSessionIdentity, checkpointId: string, experimentId: string) => {
    applyWorkspaceAction({
      type: 'promote-experiment',
      checkpointId,
      experimentId,
      promotedAt: Date.now(),
    });
    onInspectSession?.(child);
  };
  const terminalInput = (data: string) => {
    try {
      terminalRef.current?.input(data);
      setError('');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Terminal input failed.');
    }
  };
  const terminalResize = (cols: number, rows: number) => {
    try {
      terminalRef.current?.resize(cols, rows);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Terminal resize failed.');
    }
  };
  const reconnectTerminal = () => setTerminalEpoch((current) => current + 1);
  const stopCurrentTerminal = async () => {
    if (terminalId === null) return;
    const actionIdentityKey = currentIdentityKey;
    try {
      await stopTerminal(terminalId, fetch);
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setTerminalId(null);
      setError('');
    } catch (caught) {
      if (activeIdentityKeyRef.current !== actionIdentityKey) return;
      setError(caught instanceof Error ? caught.message : 'Terminal stop failed.');
    }
  };
  const resolvePersistence = async () => {
    const actionIdentityKey = currentIdentityKey;
    await workspaceClient?.resolveWithLocal();
    if (activeIdentityKeyRef.current !== actionIdentityKey) return;
    const snapshot = workspaceClient?.snapshot();
    if (snapshot !== undefined) setPersistence(snapshot);
  };
  const reloadWorkspace = async () => {
    const actionIdentityKey = currentIdentityKey;
    workspaceWritableRef.current = false;
    await workspaceClient?.reloadFromServer();
    if (activeIdentityKeyRef.current !== actionIdentityKey) return;
    const snapshot = workspaceClient?.snapshot();
    if (snapshot === undefined) return;
    setPersistence(snapshot);
    if (snapshot.error === null) {
      try {
        const restored =
          snapshot.state === null ? defaultSessionWorkspace() : normalizeSessionWorkspace(snapshot.state);
        workspaceWritableRef.current = true;
        workspaceRef.current = restored;
        setWorkspace(restored);
        setWorkspaceFormatError('');
      } catch (caught) {
        workspaceWritableRef.current = false;
        setWorkspaceFormatError(
          caught instanceof Error ? caught.message : 'This Canvas workspace format is unsupported.',
        );
      }
    } else {
      workspaceWritableRef.current = true;
    }
  };
  const loadFile = (path: string) => loadSessionFile(identity, path, fetch);

  return (
    <SessionCanvasWorkbench
      detail={detail}
      workspace={workspace}
      persistence={persistence}
      terminal={terminal}
      analyses={analyses}
      actionError={error}
      aiDisabled={aiDisabled}
      onWorkspaceChange={commitWorkspace}
      onRename={rename}
      onLaunchTerminal={launchTerminal}
      onTerminalInput={terminalInput}
      onTerminalResize={terminalResize}
      onReconnectTerminal={reconnectTerminal}
      onStopTerminal={stopCurrentTerminal}
      onSendNote={sendNote}
      onResolvePersistence={resolvePersistence}
      onReloadWorkspace={reloadWorkspace}
      onLoadFile={loadFile}
      onAnalyze={analyze}
      onFork={fork}
      onPromote={promote}
    />
  );
}
