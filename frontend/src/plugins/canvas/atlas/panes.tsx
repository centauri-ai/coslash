/* oxlint-disable react/only-export-components -- Pane components ship with their shared field helpers. */
import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  CheckIcon,
  HandIcon,
  InfoIcon,
  LoaderCircleIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  SendIcon,
  SquareIcon,
  TerminalIcon,
  Trash2Icon,
  TriangleAlertIcon,
  UserRoundIcon,
  XIcon,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import {
  defaultSeatForVendor,
  type AtlasComponent,
  type AtlasSeat,
  type AtlasWorkerSeat,
} from '@/plugins/canvas/atlas/graph';
import {
  ATLAS_COMPONENT_STATUS_LABEL,
  ATLAS_RUN_STATUS_LABEL,
  elapsedLabel,
  isRefineSeat,
  type AtlasAttemptControls,
  type AtlasCommitteeProgress,
  type AtlasGateView,
} from '@/plugins/canvas/atlas/runs';
import { terminalKeyData, type TerminalConnectionSnapshot } from '@/plugins/canvas/atlas/terminal';
import type {
  AtlasAttemptState,
  AtlasComponentRunState,
  AtlasPublicationRecord,
  AtlasPublishPreflight,
  AtlasRun,
} from '@/plugins/canvas/atlas/types';
import {
  ATLAS_MAX_WORKERS,
  effortsFor,
  modelsFor,
  permissionsFor,
  type AtlasVendor,
} from '@/plugins/canvas/atlas/vocabulary';

const VENDOR_LABELS: Record<AtlasVendor, string> = { claude: 'Claude Code', codex: 'Codex' };

function stopClick(event: { stopPropagation: () => void }): void {
  event.stopPropagation();
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="atlas-field">
      <span>{label}</span>
      {children}
    </label>
  );
}

function Select({
  label,
  value,
  options,
  labels,
  onChange,
}: {
  label: string;
  value: string;
  options: readonly string[];
  labels?: Record<string, string>;
  onChange: (value: string) => void;
}) {
  return (
    <Field label={label}>
      <select value={value} aria-label={label} onChange={(event) => onChange(event.target.value)}>
        {options.map((option) => (
          <option key={option} value={option}>
            {labels?.[option] ?? option}
          </option>
        ))}
      </select>
    </Field>
  );
}

export function RunStateStrip({ runState }: { runState: AtlasComponentRunState }) {
  const failed = runState.status === 'failed';
  const reason = runState.reason ?? '';
  const showReason =
    reason !== '' && (failed || runState.status === 'awaiting_approval' || runState.status === 'blocked');
  return (
    <div className={cn('atlas-strip', { 'atlas-strip-failed': failed })}>
      <div className="flex items-center gap-2 text-[11px]">
        <span
          className={cn('size-1.5 rounded-full', {
            'bg-success': runState.status === 'succeeded',
            'bg-brand animate-pulse': runState.status === 'running' || runState.status === 'validating',
            'bg-destructive': failed,
            'bg-warning': runState.status === 'ready' || runState.status === 'awaiting_approval',
            'bg-muted-foreground': runState.status === 'blocked',
          })}
        />
        <span className="font-semibold">{ATLAS_COMPONENT_STATUS_LABEL[runState.status]}</span>
        {runState.instance > 1 && <span className="text-muted-foreground">round {runState.instance}</span>}
      </div>
      {showReason && (
        <div className={cn('text-[10px]', failed ? 'text-destructive' : 'text-muted-foreground')}>
          <span className="font-mono">{reason}</span>
          {runState.message ? ` — ${runState.message}` : null}
        </div>
      )}
    </div>
  );
}

/**
 * How the committee is doing, in the terms the operator configured it in.
 *
 * A stage that finished with fewer drafts than seats says so: rolling that up
 * as plain success would hide the thing the fan-out exists to reveal.
 */
export function CommitteeProgressStrip({ progress }: { progress: AtlasCommitteeProgress }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px]">
      <span className="text-muted-foreground font-semibold tracking-widest uppercase">Committee</span>
      <span>
        {progress.finished} of {progress.workers} drafted
      </span>
      {progress.running > 0 && <span className="text-brand">{progress.running} running</span>}
      {progress.refining && <span className="text-brand">consolidating</span>}
      {progress.partial && (
        <span className="text-warning" role="status">
          partial — some seats produced no draft
        </span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Committee configuration
// ---------------------------------------------------------------------------

function SeatFields({ seat, onChange }: { seat: AtlasSeat; onChange: (seat: AtlasSeat) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2">
      <Select
        label="Agent"
        value={seat.vendor}
        options={['claude', 'codex']}
        labels={VENDOR_LABELS}
        // A vendor switch invalidates the whole vocabulary, so the seat is
        // replaced with that vendor's complete default rather than left in a
        // tuple the server would refuse.
        onChange={(vendor) => onChange(defaultSeatForVendor(vendor as AtlasVendor))}
      />
      <Select
        label="Model"
        value={seat.model}
        options={modelsFor(seat.vendor)}
        onChange={(model) => {
          const allowed = effortsFor(seat.vendor, model);
          onChange({ ...seat, model, effort: allowed.includes(seat.effort) ? seat.effort : allowed[0] });
        }}
      />
      <Select
        label="Effort"
        value={seat.effort}
        options={effortsFor(seat.vendor, seat.model)}
        onChange={(effort) => onChange({ ...seat, effort })}
      />
      <Select
        label={seat.vendor === 'codex' ? 'Sandbox' : 'Permission'}
        value={seat.permission}
        options={permissionsFor(seat.vendor)}
        onChange={(permission) => onChange({ ...seat, permission })}
      />
    </div>
  );
}

export type CommitteeEditorProps = {
  component: AtlasComponent;
  editable: boolean;
  onResize: (size: number) => void;
  onWorkerChange: (workerId: string, seat: AtlasSeat) => void;
  onConsolidationPromptChange: (prompt: string) => void;
};

export function CommitteeEditor({
  component,
  editable,
  onResize,
  onWorkerChange,
  onConsolidationPromptChange,
}: CommitteeEditorProps) {
  const size = component.seats.length;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
          Committee · {size} {size === 1 ? 'seat' : 'seats'}
        </span>
        {editable && (
          <span className="flex items-center gap-1">
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Remove a committee seat"
              disabled={size <= 1}
              onClick={() => onResize(size - 1)}
            >
              <Trash2Icon />
            </Button>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Add a committee seat"
              disabled={size >= ATLAS_MAX_WORKERS}
              onClick={() => onResize(size + 1)}
            >
              <PlusIcon />
            </Button>
          </span>
        )}
      </div>

      {size === 1 && (
        <p className="text-muted-foreground text-[10px]">
          One seat writes the result directly. Add a seat to have several draft independently and a main seat
          consolidate them.
        </p>
      )}

      {component.seats.map((worker: AtlasWorkerSeat) => (
        <div key={worker.id} className="flex flex-col gap-1.5 rounded-md border p-2">
          <div className="flex items-center gap-2 text-[10px]">
            <span className="font-mono">{worker.id}</span>
            {worker.role === 'main' && (
              <span className="text-brand font-semibold tracking-widest uppercase">main</span>
            )}
          </div>
          {editable ? (
            <SeatFields
              seat={{
                vendor: worker.vendor,
                model: worker.model,
                effort: worker.effort,
                permission: worker.permission,
              }}
              onChange={(seat) => onWorkerChange(worker.id, seat)}
            />
          ) : (
            <div className="text-muted-foreground text-[10px]">
              {VENDOR_LABELS[worker.vendor]} · {worker.model} · {worker.effort}
            </div>
          )}
          {worker.permission === 'bypassPermissions' && (
            <div className="text-muted-foreground flex items-start gap-1.5 text-[10px]">
              <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
              <span>Claude has no sandbox — this grants a shell with your full permissions.</span>
            </div>
          )}
        </div>
      ))}

      {size > 1 && (
        <Field label="Consolidation steering">
          <textarea
            value={component.consolidationPrompt}
            aria-label="Consolidation steering"
            placeholder="How should the main seat choose between drafts?"
            disabled={!editable}
            onChange={(event) => onConsolidationPromptChange(event.target.value)}
            className="min-h-16 resize-none"
          />
        </Field>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Gates
// ---------------------------------------------------------------------------

export type GateDecider = (
  decision: 'approved' | 'rejected',
  options?: { publish?: boolean },
) => Promise<void>;

function StaleGateNotice({ gate }: { gate: AtlasGateView }) {
  return (
    <div role="alert" className="text-warning flex items-start gap-1.5 text-[10px]">
      <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
      <span>{gate.staleReason}</span>
    </div>
  );
}

/** A manual trigger edge waiting for the operator's go. */
export function TriggerGatePane({ gate, onDecide }: { gate: AtlasGateView; onDecide: GateDecider }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const settled = `${gate.open}:${gate.decidable}`;
  useEffect(() => setBusy(false), [settled]);

  if (!gate.open || gate.reason !== 'waiting_for_trigger') return null;

  async function decide(decision: 'approved' | 'rejected') {
    setBusy(true);
    setError('');
    try {
      await onDecide(decision);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The decision failed.');
      setBusy(false);
    }
  }

  return (
    <div className="atlas-panel">
      <div className="text-[11px] font-semibold">Waiting for your go</div>
      <div className="text-muted-foreground text-[11px]">{gate.message}</div>
      {!gate.decidable && gate.staleReason !== '' && <StaleGateNotice gate={gate} />}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button
          size="xs"
          variant="ghost"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('rejected')}
        >
          Stop the run
        </Button>
        <Button size="xs" disabled={busy || !gate.decidable} onClick={() => void decide('approved')}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
          Go
        </Button>
      </div>
      {error !== '' && (
        <div role="alert" className="atlas-error">
          {error}
        </div>
      )}
    </div>
  );
}

export function RepairGatePane({ gate, onDecide }: { gate: AtlasGateView; onDecide: GateDecider }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const settled = `${gate.open}:${gate.reason}:${gate.decidable}`;
  useEffect(() => setBusy(false), [settled]);

  if (!gate.open || gate.reason !== 'waiting_for_repair') return null;

  async function decide(decision: 'approved' | 'rejected') {
    setBusy(true);
    setError('');
    try {
      await onDecide(decision);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The decision failed.');
      setBusy(false);
    }
  }

  return (
    <div className="atlas-panel">
      <div className="text-[11px] font-semibold">Repair rounds exhausted</div>
      <div className="text-muted-foreground text-[11px]">{gate.message}</div>
      {!gate.decidable && gate.staleReason !== '' && <StaleGateNotice gate={gate} />}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button
          size="xs"
          variant="ghost"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('rejected')}
        >
          Reject run
        </Button>
        <Button size="xs" disabled={busy || !gate.decidable} onClick={() => void decide('approved')}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
          Run one more round
        </Button>
      </div>
      {error !== '' && (
        <div role="alert" className="atlas-error">
          {error}
        </div>
      )}
    </div>
  );
}

export function PublishGatePane({
  gate,
  publication,
  unavailableReason,
  onReadPreflight,
  onDecide,
}: {
  gate: AtlasGateView;
  publication: AtlasPublicationRecord | null;
  /** Non-empty when the project cannot publish at all, e.g. a plain folder. */
  unavailableReason: string;
  onReadPreflight: () => Promise<AtlasPublishPreflight>;
  onDecide: GateDecider;
}) {
  const [preflight, setPreflight] = useState<AtlasPublishPreflight | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const settled = `${gate.open}:${gate.decidable}:${publication?.commitSha ?? ''}`;
  useEffect(() => setBusy(false), [settled]);

  const open = gate.open;
  const publishable = unavailableReason === '';
  useEffect(() => {
    let active = true;
    if (!open || !publishable) {
      setPreflight(null);
      return;
    }
    setError('');
    void (async () => {
      try {
        const result = await onReadPreflight();
        if (active) setPreflight(result);
      } catch (caught) {
        if (active) setError(caught instanceof Error ? caught.message : 'The preflight is unavailable.');
      }
    })();
    return () => {
      active = false;
    };
  }, [open, publishable, onReadPreflight]);

  async function decide(decision: 'approved' | 'rejected', options?: { publish?: boolean }) {
    setBusy(true);
    setError('');
    try {
      await onDecide(decision, options);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The gate decision failed.');
      setBusy(false);
    }
  }

  if (publication !== null) {
    return (
      <div className="atlas-panel text-[11px]">
        <div className="font-semibold">Published</div>
        <div className="text-muted-foreground font-mono">
          rev {publication.changeRevision} · {publication.commitSha.slice(0, 12)} · {publication.action}
        </div>
        {publication.prUrl ? (
          <a
            href={publication.prUrl}
            target="_blank"
            rel="noreferrer"
            className="text-brand underline-offset-2 hover:underline"
          >
            {publication.prNumber != null ? `PR #${publication.prNumber}` : 'Pull request'}
          </a>
        ) : (
          <div className="text-muted-foreground">No pull request URL was recorded.</div>
        )}
      </div>
    );
  }

  if (!open) return null;

  return (
    <div className="atlas-panel">
      <div className="text-[11px] font-semibold">Awaiting your approval</div>
      {!gate.decidable && gate.staleReason !== '' && <StaleGateNotice gate={gate} />}

      {/* A plain folder is a supported project, not a broken one, so the gate
          explains what is unavailable rather than disappearing. */}
      {!publishable && (
        <div className="atlas-banner" role="status">
          <InfoIcon className="mt-0.5 size-3 shrink-0" />
          <span>{unavailableReason}</span>
        </div>
      )}

      {publishable && preflight === null && error === '' && (
        <div className="text-muted-foreground flex items-center gap-2 text-[11px]">
          <LoaderCircleIcon className="size-3.5 animate-spin" />
          Checking preflight…
        </div>
      )}
      {preflight && (
        <div className="flex flex-col gap-1.5">
          {preflight.checklist.map((item) => (
            <div key={item.id} className="flex items-start gap-2 text-[11px]">
              {item.ok ? (
                <CheckIcon className="text-success mt-0.5 size-3.5 shrink-0" />
              ) : (
                <XIcon className="text-destructive mt-0.5 size-3.5 shrink-0" />
              )}
              <div className="min-w-0">
                <div className={cn({ 'text-destructive': !item.ok })}>{item.label}</div>
                {!item.ok && <div className="text-muted-foreground text-[10px]">{item.detail}</div>}
              </div>
            </div>
          ))}
          <div className="text-muted-foreground pt-1 text-[10px]">
            → commit · push {preflight.branch}
            {preflight.draft ? ' · open 1 draft PR' : ' · open 1 PR'}
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button
          size="xs"
          variant="ghost"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('rejected')}
        >
          Reject
        </Button>
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            size="xs"
            variant="outline"
            disabled={busy || !gate.decidable}
            title="Mark the run done without commit, push, or pull request"
            onClick={() => void decide('approved', { publish: false })}
          >
            Approve without publish
          </Button>
          <Button
            size="xs"
            disabled={busy || !gate.decidable || !publishable || preflight?.ok !== true}
            onClick={() => void decide('approved', { publish: true })}
          >
            {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
            Approve &amp; publish
          </Button>
        </div>
      </div>
      {error !== '' && (
        <div role="alert" className="atlas-error">
          {error}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Seat terminal
// ---------------------------------------------------------------------------

export type CommitteeTerminalProps = {
  componentId: string;
  runState: AtlasComponentRunState | null;
  visible: boolean;
  now: number;
  /** The attempt whose terminal is shown; null selects the first live one. */
  selectedAttemptId: string | null;
  terminals: Record<string, { snapshot: TerminalConnectionSnapshot | null; error: string }>;
  controlsFor: (attemptId: string) => AtlasAttemptControls;
  onSelectAttempt: (attemptId: string) => void;
  onInput: (attemptId: string, data: string) => void;
  onReconnect: (attemptId: string) => void;
  onTakeControl: (attemptId: string) => Promise<void>;
  onHandBack: (attemptId: string) => Promise<void>;
  onRetry: () => Promise<void>;
  onCancel: () => Promise<void>;
  canRetry: boolean;
  canCancel: boolean;
};

/**
 * A committee's terminals.
 *
 * Every sibling is listed, because the operator configured a committee to see
 * several independent turns; showing only the newest would make the fan-out
 * invisible at exactly the moment it matters.
 */
export function CommitteeTerminalPane(props: CommitteeTerminalProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const attempts = props.runState?.attempts ?? [];
  const settled = attempts
    .map((attempt) => `${attempt.attemptId}:${attempt.status}:${attempt.ownership}`)
    .join('|');
  useEffect(() => setBusy(false), [settled]);

  if (!props.visible) {
    return (
      <div className="atlas-terminal-empty">
        <TerminalIcon className="size-4" />
        <span>The committee's terminals open here when this stage runs</span>
        <span className="text-[10px]">Configure the seats on the info card · steering beside it</span>
      </div>
    );
  }

  const selected =
    attempts.find((attempt) => attempt.attemptId === props.selectedAttemptId) ??
    attempts.find((attempt) => attempt.status !== 'exited') ??
    attempts.at(-1) ??
    null;

  async function run(action: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : fallback);
      setBusy(false);
    }
  }

  return (
    <div className="atlas-committee" onClick={stopClick}>
      <div className="atlas-committee-header">
        <span className="truncate">{attempts.length} seat turns</span>
        <span className="ml-auto flex items-center gap-0.5">
          {props.canCancel && (
            <Button
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(props.onCancel, 'Cancel failed.')}
            >
              <SquareIcon />
              Cancel
            </Button>
          )}
          {props.canRetry && (
            <Button
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(props.onRetry, 'Retry failed.')}
            >
              <RotateCcwIcon />
              Retry committee
            </Button>
          )}
        </span>
      </div>

      <div className="flex flex-wrap gap-1 px-2 pb-1">
        {attempts.map((attempt) => (
          <button
            key={attempt.attemptId}
            type="button"
            aria-label={`Show ${attempt.seatId}`}
            className={cn(
              'rounded border px-1.5 py-0.5 font-mono text-[10px]',
              attempt.attemptId === selected?.attemptId
                ? 'border-brand text-foreground'
                : 'text-muted-foreground',
            )}
            onClick={() => props.onSelectAttempt(attempt.attemptId)}
          >
            {attempt.seatId}
            {attempt.status !== 'exited' ? ' ·' : ''}
          </button>
        ))}
      </div>

      {selected !== null && (
        <AttemptTerminal
          attempt={selected}
          refine={isRefineSeat(props.componentId, selected.seatId)}
          controls={props.controlsFor(selected.attemptId)}
          terminal={props.terminals[selected.attemptId]?.snapshot ?? null}
          attachError={props.terminals[selected.attemptId]?.error ?? ''}
          now={props.now}
          busy={busy}
          onInput={(data) => props.onInput(selected.attemptId, data)}
          onReconnect={() => props.onReconnect(selected.attemptId)}
          onTakeControl={() => run(() => props.onTakeControl(selected.attemptId), 'Takeover failed.')}
          onHandBack={() => run(() => props.onHandBack(selected.attemptId), 'Handback failed.')}
        />
      )}

      {error !== '' && (
        <div role="alert" className="atlas-error border-t px-2 py-1">
          {error}
        </div>
      )}
    </div>
  );
}

function AttemptTerminal({
  attempt,
  refine,
  controls,
  terminal,
  attachError,
  now,
  busy,
  onInput,
  onReconnect,
  onTakeControl,
  onHandBack,
}: {
  attempt: AtlasAttemptState;
  refine: boolean;
  controls: AtlasAttemptControls;
  terminal: TerminalConnectionSnapshot | null;
  attachError: string;
  now: number;
  busy: boolean;
  onInput: (data: string) => void;
  onReconnect: () => void;
  onTakeControl: () => Promise<void>;
  onHandBack: () => Promise<void>;
}) {
  const outputRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    const output = outputRef.current;
    if (output !== null) output.scrollTop = output.scrollHeight;
  }, [terminal?.output]);

  const elapsed = elapsedLabel(attempt.startedAt, attempt.finishedAt, now);
  return (
    <div className={cn('atlas-committee-member', { 'atlas-committee-member-refine': refine })}>
      <div className="atlas-terminal-status">
        <span className="truncate">
          {attempt.seatId}
          {refine ? ' · consolidating' : ''}
          {` · attempt ${attempt.attempt}`}
          {elapsed ? ` · ${elapsed}` : ''}
          {` · ${attempt.ownership}`}
          {attempt.exitCode != null ? ` · exit ${attempt.exitCode}` : ''}
          {terminal ? ` · ${terminal.status}` : ''}
        </span>
        <span className="atlas-terminal-actions">
          {controls.canTakeControl && (
            <Button size="xs" variant="ghost" disabled={busy} onClick={() => void onTakeControl()}>
              <UserRoundIcon />
              Take control
            </Button>
          )}
          {controls.canHandBack && (
            <Button size="xs" variant="ghost" disabled={busy} onClick={() => void onHandBack()}>
              <HandIcon />
              Return
            </Button>
          )}
          {terminal !== null && (terminal.status === 'error' || terminal.status === 'closed') && (
            <Button size="xs" variant="ghost" onClick={onReconnect}>
              <RefreshCwIcon />
              Reconnect
            </Button>
          )}
        </span>
      </div>
      <pre
        ref={outputRef}
        role="log"
        aria-label={`${attempt.seatId} terminal`}
        tabIndex={terminal?.status === 'open' ? 0 : -1}
        onKeyDown={(event) => {
          if (terminal?.status !== 'open') return;
          const data = terminalKeyData(event);
          if (data === null) return;
          event.preventDefault();
          onInput(data);
        }}
      >
        {terminal?.output ??
          (attachError !== ''
            ? attachError
            : attempt.status === 'exited'
              ? 'This terminal session ended.'
              : 'Connecting to the seat terminal…')}
      </pre>
    </div>
  );
}

// ---------------------------------------------------------------------------
// The run rail
// ---------------------------------------------------------------------------

export type RunRailProps = {
  run: AtlasRun;
  /** Stages the graph has no node for: intake, verify, publish. */
  stages: readonly AtlasComponentRunState[];
  /** The open gate none of the graph's seats owns, closed when there is none. */
  gate: AtlasGateView;
  publicationUnavailable: string;
  canCancel: boolean;
  onReadPreflight: () => Promise<AtlasPublishPreflight>;
  onDecide: GateDecider;
  onCancel: () => Promise<void>;
};

/**
 * The deterministic half of a run.
 *
 * Intake, Verify, and Publish have no seat to configure, so they have no node
 * on the graph. They still run, still fail, and still open the publish gate —
 * this rail is where the operator sees and decides them.
 */
export function RunRail(props: RunRailProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const { run } = props;
  useEffect(() => setBusy(false), [run.status, run.lastSeq]);

  async function cancel() {
    setBusy(true);
    setError('');
    try {
      await props.onCancel();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The run could not be canceled.');
      setBusy(false);
    }
  }

  return (
    <div className="atlas-rail" onClick={stopClick}>
      <div className="atlas-rail-header">
        <span className="flex-1 truncate font-semibold">{run.title}</span>
        <span className="text-muted-foreground">{ATLAS_RUN_STATUS_LABEL[run.status]}</span>
        {props.canCancel && (
          <Button size="xs" variant="ghost" disabled={busy} onClick={() => void cancel()}>
            <SquareIcon />
            Cancel run
          </Button>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-1">
        {props.stages.map((stage) => (
          <span
            key={stage.id}
            className={cn('atlas-rail-stage', `atlas-rail-stage-${stage.status}`)}
            title={stage.message || stage.reason || ''}
          >
            <span className="font-mono">{stage.id}</span>
            <span className="text-muted-foreground">{ATLAS_COMPONENT_STATUS_LABEL[stage.status]}</span>
          </span>
        ))}
      </div>

      {run.change !== null && (
        <div className="text-muted-foreground text-[10px]">
          rev {run.change.changeRevision} · {run.change.changedFiles.length}{' '}
          {run.change.changedFiles.length === 1 ? 'file' : 'files'} · +{run.change.insertions} −
          {run.change.deletions}
        </div>
      )}

      {/* A failed run names the taxonomy reason rather than a generic banner:
          the reason is what tells the operator whether a retry can help. */}
      {run.failure !== null && (
        <div role="alert" className="atlas-error">
          <span className="font-mono">{run.failure.reason}</span>
          {run.failure.message ? ` — ${run.failure.message}` : null}
        </div>
      )}

      <TriggerGatePane gate={props.gate} onDecide={props.onDecide} />
      <RepairGatePane gate={props.gate} onDecide={props.onDecide} />
      {(props.gate.reason === 'blocked_by_gate' || run.publication !== null) && (
        <PublishGatePane
          gate={props.gate}
          publication={run.publication}
          unavailableReason={props.publicationUnavailable}
          onReadPreflight={props.onReadPreflight}
          onDecide={props.onDecide}
        />
      )}

      {error !== '' && (
        <div role="alert" className="atlas-error">
          {error}
        </div>
      )}
    </div>
  );
}

export type SeatPromptPaneProps = {
  component: AtlasComponent;
  editable: boolean;
  canSend: boolean;
  draft: string;
  onPromptChange: (prompt: string) => void;
  onDraftChange: (draft: string) => void;
  onSend: (text: string) => Promise<void>;
};

export function SeatPromptPane({
  component,
  editable,
  canSend,
  draft,
  onPromptChange,
  onDraftChange,
  onSend,
}: SeatPromptPaneProps) {
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');

  async function send() {
    const text = draft.trim();
    if (!canSend || text === '' || sending) return;
    setSending(true);
    setError('');
    try {
      await onSend(text);
      onDraftChange('');
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The message could not be delivered.');
    } finally {
      setSending(false);
    }
  }

  if (editable) {
    return (
      <div className="atlas-prompt" onClick={stopClick}>
        <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
          Prompt card
        </span>
        <textarea
          value={component.prompt}
          aria-label={`${component.title} prompt card`}
          placeholder="Steer this seat — it cannot change what counts as done…"
          onChange={(event) => onPromptChange(event.target.value)}
        />
      </div>
    );
  }

  return (
    <div className="atlas-prompt" onClick={stopClick}>
      <textarea
        value={draft}
        aria-label={`${component.title} seat message`}
        placeholder={canSend ? 'Message this seat — Enter to send' : 'Take control of a seat to send…'}
        onChange={(event) => onDraftChange(event.target.value)}
        onKeyDown={(event) => {
          if (canSend && event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void send();
          }
        }}
      />
      <div className="atlas-prompt-footer">
        <span className="text-muted-foreground">
          {error !== '' ? (
            <span className="text-destructive" role="alert">
              {error}
            </span>
          ) : canSend ? null : (
            'Automated turn — take control to send'
          )}
        </span>
        <Button size="xs" disabled={!canSend || sending || draft.trim() === ''} onClick={() => void send()}>
          {sending ? <LoaderCircleIcon className="animate-spin" /> : <SendIcon />}
          Send
        </Button>
      </div>
    </div>
  );
}
