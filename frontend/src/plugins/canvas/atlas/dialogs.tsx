/* oxlint-disable react/only-export-components -- The dialog module exports its components together. */
import { useEffect, useState } from 'react';
import {
  CheckIcon,
  ChevronDownIcon,
  CircleDotIcon,
  FolderOpenIcon,
  LayersIcon,
  LoaderCircleIcon,
  PlayIcon,
  PlusIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import type { AtlasBoardSummary } from '@/plugins/canvas/atlas/api';
import type { AtlasBoard } from '@/plugins/canvas/atlas/graph';
import { ATLAS_RUN_STATUS_LABEL } from '@/plugins/canvas/atlas/runs';
import type {
  AtlasRun,
  AtlasRunPreview,
  AtlasRunStatus,
  AtlasRunSummary,
  AtlasSourceInput,
} from '@/plugins/canvas/atlas/types';

// ---------------------------------------------------------------------------
// Run selection
// ---------------------------------------------------------------------------

function StatusDot({ status }: { status: AtlasRunStatus }) {
  if (status === 'preparing' || status === 'running')
    return <LoaderCircleIcon className="text-brand size-3 animate-spin" />;
  if (status === 'succeeded') return <CheckIcon className="text-success size-3" />;
  if (status === 'failed') return <XIcon className="text-destructive size-3" />;
  return <CircleDotIcon className="text-muted-foreground size-3" />;
}

/**
 * The run selector.
 *
 * A chip rather than a tab row because a workflow can have many runs and only
 * one of them is being watched — the point is to say which.
 */
export function RunChip({
  activeRun,
  runs,
  onSelect,
}: {
  activeRun: AtlasRun | null;
  runs: readonly AtlasRunSummary[];
  onSelect: (runId: string | null) => void;
}) {
  if (runs.length === 0 && activeRun === null) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="xs" title={activeRun?.runId ?? 'Runs of this workflow'}>
          {activeRun ? <StatusDot status={activeRun.status} /> : <CircleDotIcon />}
          <span className="max-w-40 truncate">{activeRun ? activeRun.title : 'Runs'}</span>
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        <DropdownMenuLabel>Runs</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {runs.length === 0 ? (
          <DropdownMenuItem disabled>No runs yet</DropdownMenuItem>
        ) : (
          runs.map((run) => (
            <DropdownMenuItem
              key={run.runId}
              className={cn('flex flex-col items-start gap-0.5', {
                'bg-accent': run.runId === activeRun?.runId,
              })}
              onSelect={() => onSelect(run.runId)}
            >
              <div className="flex w-full items-center gap-2">
                <StatusDot status={run.status} />
                <span className="flex-1 truncate">{run.title}</span>
                <span className="text-muted-foreground text-[10px]">
                  {ATLAS_RUN_STATUS_LABEL[run.status]}
                </span>
              </div>
              <span className="text-muted-foreground font-mono text-[10px]">{run.runId}</span>
            </DropdownMenuItem>
          ))
        )}
        {activeRun !== null && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => onSelect(null)}>Stop watching this run</DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ---------------------------------------------------------------------------
// Workflow management
// ---------------------------------------------------------------------------

export function BoardMenu({
  boards,
  activeBoardId,
  projectReady,
  onOpen,
  onDelete,
  onNew,
  onSaveAs,
}: {
  boards: readonly AtlasBoardSummary[];
  activeBoardId: string | null;
  projectReady: boolean;
  onOpen: (summary: AtlasBoardSummary) => void;
  onDelete: (summary: AtlasBoardSummary) => void;
  onNew: () => void;
  onSaveAs: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="xs" disabled={!projectReady} title="Workflows saved in this project">
          <LayersIcon />
          <span className="max-w-40 truncate">
            {boards.find((board) => board.id === activeBoardId)?.name ?? 'Workflows'}
          </span>
          <ChevronDownIcon />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80">
        <DropdownMenuLabel>Workflows</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {boards.length === 0 ? (
          <DropdownMenuItem disabled>No saved workflows</DropdownMenuItem>
        ) : (
          boards.map((board) => (
            <DropdownMenuItem
              key={board.id}
              className={cn('flex items-center gap-2', { 'bg-accent': board.id === activeBoardId })}
              onSelect={() => onOpen(board)}
            >
              <span className="flex-1 truncate">{board.name}</span>
              <span className="text-muted-foreground text-[10px]">rev {board.revision}</span>
              <Button
                variant="ghost"
                size="icon-xs"
                className="text-muted-foreground hover:text-destructive"
                aria-label={`Delete ${board.name}`}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  onDelete(board);
                }}
              >
                <Trash2Icon />
              </Button>
            </DropdownMenuItem>
          ))
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onSaveAs}>Save as a new workflow…</DropdownMenuItem>
        <DropdownMenuItem onSelect={onNew}>
          <PlusIcon />
          New workflow
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function SaveBoardDialog({
  open,
  busy,
  onOpenChange,
  onSave,
}: {
  open: boolean;
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) {
      setName('');
      setError('');
    }
  }, [open]);

  async function save() {
    if (name.trim() === '' || busy) return;
    setError('');
    try {
      await onSave(name.trim());
      onOpenChange(false);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The workflow could not be saved.');
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Save workflow</DialogTitle>
          <DialogDescription>
            A run is pinned to a saved workflow revision. Name it once here; later edits autosave.
          </DialogDescription>
        </DialogHeader>
        <Input
          value={name}
          placeholder="e.g. Parser refactor"
          maxLength={200}
          aria-label="Workflow name"
          onChange={(event) => setName(event.target.value)}
        />
        {error !== '' && (
          <div role="alert" className="text-destructive text-xs">
            {error}
          </div>
        )}
        <DialogFooter>
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={busy || name.trim() === ''} onClick={() => void save()}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ProjectDialog({
  open,
  initialPath,
  suggestions,
  busy,
  onOpenChange,
  onChoose,
}: {
  open: boolean;
  initialPath: string;
  suggestions: readonly string[];
  busy: boolean;
  onOpenChange: (open: boolean) => void;
  onChoose: (projectPath: string) => Promise<void>;
}) {
  const [path, setPath] = useState(initialPath);
  const [error, setError] = useState('');

  useEffect(() => {
    if (open) {
      setPath(initialPath);
      setError('');
    }
  }, [open, initialPath]);

  async function choose(candidate: string) {
    const target = candidate.trim();
    if (target === '' || busy) return;
    setError('');
    try {
      await onChoose(target);
      onOpenChange(false);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'That project could not be opened.');
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Choose project</DialogTitle>
          <DialogDescription>
            Workflows are stored with the project, under{' '}
            <span className="font-mono">.coslash/atlas/boards</span>. Runs execute in their own clone, so this
            folder&apos;s working tree is never touched.
          </DialogDescription>
        </DialogHeader>
        <Input
          value={path}
          placeholder="/absolute/path/to/project"
          spellCheck={false}
          aria-label="Project path"
          onChange={(event) => setPath(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') void choose(path);
          }}
        />
        {suggestions.length > 0 && (
          <div className="flex flex-col gap-1">
            <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
              From your sessions
            </span>
            {suggestions.map((suggestion) => (
              <button
                key={suggestion}
                type="button"
                className="hover:bg-muted truncate rounded px-2 py-1 text-left font-mono text-xs"
                onClick={() => void choose(suggestion)}
              >
                {suggestion}
              </button>
            ))}
          </div>
        )}
        {error !== '' && (
          <div role="alert" className="text-destructive text-xs">
            {error}
          </div>
        )}
        <DialogFooter>
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={busy || path.trim() === ''} onClick={() => void choose(path)}>
            <FolderOpenIcon />
            Open project
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

/** Committee seats across the whole graph, so the dialog can say what it costs. */
function committeeSummary(board: AtlasBoard): { seats: number; committees: number } {
  let seats = 0;
  let committees = 0;
  for (const component of board.components) {
    seats += component.seats.length;
    if (component.seats.length > 1) committees += 1;
  }
  return { seats, committees };
}

export function RunDialog({
  open,
  board,
  boardId,
  boardName,
  needsSave,
  starting,
  onOpenChange,
  onPreview,
  onStart,
}: {
  open: boolean;
  board: AtlasBoard;
  boardId: string | null;
  boardName: string;
  needsSave: boolean;
  starting: boolean;
  onOpenChange: (open: boolean) => void;
  onPreview: (input: { boardId: string } | { board: AtlasBoard }) => Promise<AtlasRunPreview>;
  onStart: (input: { source: AtlasSourceInput; boardName?: string }) => Promise<void>;
}) {
  const [kind, setKind] = useState<'text' | 'file'>('text');
  const [title, setTitle] = useState('');
  const [text, setText] = useState('');
  const [filePath, setFilePath] = useState('');
  const [saveName, setSaveName] = useState('');
  const [preview, setPreview] = useState<AtlasRunPreview | null>(null);
  const [previewError, setPreviewError] = useState('');
  const [error, setError] = useState('');
  const [pending, setPending] = useState(false);
  const busy = starting || pending;

  // Preflight only reads the publish base from the board, so the effect is
  // pinned to that: unrelated seat edits must not spam preview while open.
  const draftBase = board.runPolicy?.publish.base ?? '';
  const { seats, committees } = committeeSummary(board);

  useEffect(() => {
    if (!open) return;
    setError('');
    setPreview(null);
    setPreviewError('');
    if (needsSave) setSaveName('');
    let active = true;
    void (async () => {
      try {
        const result = await onPreview(boardId != null ? { boardId } : { board });
        if (active) setPreview(result);
      } catch (caught) {
        if (active) setPreviewError(caught instanceof Error ? caught.message : 'Preflight failed.');
      }
    })();
    return () => {
      active = false;
    };
    // `board` is intentionally omitted: only the publish base affects the
    // preflight, and depending on the object would re-run on every keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, boardId, draftBase, needsSave, onPreview]);

  const sourceReady = kind === 'text' ? text.trim() !== '' : filePath.trim() !== '';
  const canStart =
    title.trim() !== '' && sourceReady && (!needsSave || saveName.trim() !== '') && preview !== null && !busy;

  async function start() {
    if (!canStart) return;
    setError('');
    setPending(true);
    try {
      await onStart({
        source:
          kind === 'text'
            ? { kind: 'text', title: title.trim(), text }
            : { kind: 'file', title: title.trim(), path: filePath.trim() },
        boardName: needsSave ? saveName.trim() : undefined,
      });
      onOpenChange(false);
      setTitle('');
      setText('');
      setFilePath('');
      setSaveName('');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'This run could not be started.');
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>Start run</DialogTitle>
          <DialogDescription>
            {needsSave
              ? 'Set the source for this workflow. You will name and save it when you start the run.'
              : `Set the source for ${boardName}. It runs in its own clone of the project — your working tree is never touched.`}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {needsSave && (
            <label className="flex flex-col gap-2 text-sm">
              Workflow name
              <Input
                value={saveName}
                placeholder="e.g. Parser refactor"
                maxLength={200}
                aria-label="Workflow name"
                onChange={(event) => setSaveName(event.target.value)}
              />
            </label>
          )}

          <label className="flex flex-col gap-2 text-sm">
            Title
            <Input
              value={title}
              placeholder="Add a logout button"
              maxLength={200}
              aria-label="Run title"
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>

          <div className="flex flex-col gap-2">
            <span className="text-sm">Source</span>
            <div className="flex gap-2">
              {(['text', 'file'] as const).map((option) => (
                <Button
                  key={option}
                  type="button"
                  variant={kind === option ? 'secondary' : 'ghost'}
                  size="xs"
                  onClick={() => setKind(option)}
                >
                  {option === 'text' ? 'Typed text' : 'Local file'}
                </Button>
              ))}
            </div>
            {kind === 'text' ? (
              <textarea
                value={text}
                rows={7}
                aria-label="Run source text"
                placeholder="Describe the problem. This is snapshotted verbatim as SOURCE.md."
                className="focus:border-brand w-full rounded-md border bg-transparent p-2 text-sm outline-none"
                onChange={(event) => setText(event.target.value)}
              />
            ) : (
              <Input
                value={filePath}
                placeholder="/absolute/path/to/ticket.md"
                aria-label="Run source file"
                onChange={(event) => setFilePath(event.target.value)}
              />
            )}
            <p className="text-muted-foreground text-xs">
              The source is recorded as data, not instructions. It can shape the work; it cannot grant
              permissions or skip a gate.
            </p>
          </div>

          {/* A committee costs several agent turns per stage. Say so before the
              operator commits to it rather than after the bill arrives. */}
          <div className="text-muted-foreground text-xs">
            {board.components.length} {board.components.length === 1 ? 'stage' : 'stages'} · {seats} seat
            {seats === 1 ? '' : 's'}
            {committees > 0
              ? ` · ${committees} ${committees === 1 ? 'committee drafts' : 'committees draft'} in parallel, then consolidate`
              : ''}
          </div>

          <div className="rounded-md border p-3">
            {previewError !== '' ? (
              <div role="alert" className="text-destructive flex items-start gap-2 text-xs">
                <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
                <span>{previewError}</span>
              </div>
            ) : preview ? (
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
                <dt className="text-muted-foreground">Base branch</dt>
                <dd className="font-mono">
                  {preview.baseBranch}
                  {preview.isLinkedWorktree && (
                    <span className="text-muted-foreground font-sans"> · linked worktree</span>
                  )}
                </dd>
                {preview.defaultBranch !== preview.baseBranch && (
                  <>
                    <dt className="text-muted-foreground">Repo default</dt>
                    <dd className="text-muted-foreground font-mono">{preview.defaultBranch}</dd>
                  </>
                )}
                <dt className="text-muted-foreground">Base commit</dt>
                <dd className="font-mono">{preview.baseSha.slice(0, 12)}</dd>
                <dt className="text-muted-foreground">Run clone</dt>
                <dd className="font-mono break-all">{preview.runRootParent}/…</dd>
                <dt className="text-muted-foreground">Remote</dt>
                <dd className={cn('font-mono break-all', { 'text-muted-foreground': !preview.remoteUrl })}>
                  {preview.remoteUrl ?? 'none — publishing will be unavailable'}
                </dd>
              </dl>
            ) : (
              <div className="text-muted-foreground flex items-center gap-2 text-xs">
                <LoaderCircleIcon className="size-4 animate-spin" />
                Running preflight…
              </div>
            )}
          </div>

          {error !== '' && (
            <div role="alert" className="text-destructive flex items-start gap-2 text-xs">
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={!canStart} onClick={() => void start()}>
            {busy ? <LoaderCircleIcon className="animate-spin" /> : <PlayIcon />}
            {busy ? 'Starting…' : needsSave ? 'Save & start run' : 'Start run'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A read-only view of one promoted artifact.
 *
 * The contents are rendered as preformatted TEXT, never as markdown or HTML: an
 * artifact can contain the run's untrusted source verbatim, so rendering it
 * would turn a ticket body into same-origin markup. A committee draft is more
 * exposed still — several independent agents wrote into it.
 */
export function ArtifactDialog({
  open,
  runId,
  name,
  producer,
  onOpenChange,
  onRead,
}: {
  open: boolean;
  runId: string;
  name: string;
  /** The seat that produced it, so a draft is never mistaken for the result. */
  producer: string;
  onOpenChange: (open: boolean) => void;
  onRead: () => Promise<string>;
}) {
  const [contents, setContents] = useState<string | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open) return;
    setContents(null);
    setError('');
    let active = true;
    void (async () => {
      try {
        const result = await onRead();
        if (active) setContents(result);
      } catch (caught) {
        if (active) setError(caught instanceof Error ? caught.message : 'This artifact could not be read.');
      }
    })();
    return () => {
      active = false;
    };
  }, [open, onRead]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="font-mono text-sm">{name}</DialogTitle>
          <DialogDescription className="font-mono text-xs">
            {runId}
            {producer !== '' ? ` · ${producer}` : ''}
          </DialogDescription>
        </DialogHeader>
        {error !== '' ? (
          <div role="alert" className="text-destructive flex items-start gap-2 text-xs">
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
            <span>{error}</span>
          </div>
        ) : contents === null ? (
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <LoaderCircleIcon className="size-4 animate-spin" />
            Reading…
          </div>
        ) : (
          <pre className="bg-muted max-h-[60vh] overflow-auto rounded-md p-3 font-mono text-xs whitespace-pre-wrap">
            {contents}
          </pre>
        )}
      </DialogContent>
    </Dialog>
  );
}
