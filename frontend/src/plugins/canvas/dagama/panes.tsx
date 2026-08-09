/* oxlint-disable react/only-export-components -- Pane components and their shared field helpers ship together. */
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react';
import {
  CheckIcon,
  FileTextIcon,
  HandIcon,
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
  DAGAMA_COMPONENT_META,
  defaultSeatForVendor,
  type DaGamaCheck,
  type DaGamaComponent,
  type DaGamaPublishConfig,
  type DaGamaSeat,
} from '@/plugins/canvas/dagama/board';
import {
  DAGAMA_COMPONENT_STATUS_LABEL,
  elapsedLabel,
  type DaGamaGateView,
  type DaGamaSeatControls,
} from '@/plugins/canvas/dagama/runs';
import { terminalKeyData, type TerminalConnectionSnapshot } from '@/plugins/canvas/dagama/terminal';
import type {
  DaGamaComponentRunState,
  DaGamaPublicationRecord,
  DaGamaPublishPreflight,
} from '@/plugins/canvas/dagama/types';
import {
  DAGAMA_CHECK_COMMANDS,
  effortsFor,
  modelsFor,
  permissionsFor,
  type DaGamaSeatComponentId,
  type DaGamaVendor,
} from '@/plugins/canvas/dagama/vocabulary';

const VENDOR_LABELS: Record<DaGamaVendor, string> = { claude: 'Claude Code', codex: 'Codex' };

const SEAT_TITLES: Record<DaGamaSeatComponentId, string> = {
  plan: 'Plan',
  build: 'Build',
  review: 'Review',
};

/** Stops a body click from reaching the node chrome's select handler. */
function stopClick(event: { stopPropagation: () => void }): void {
  event.stopPropagation();
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="dagama-field">
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

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

export function RunStateStrip({ runState }: { runState: DaGamaComponentRunState }) {
  const failed = runState.status === 'failed';
  // The taxonomy reason is shown for failures and for gates or blocks too, so
  // `awaiting_approval` never hides why the card is waiting on the operator.
  const showReason =
    runState.reason != null &&
    runState.reason !== '' &&
    (failed || runState.status === 'awaiting_approval' || runState.status === 'blocked');
  return (
    <div className={cn('dagama-strip', { 'dagama-strip-failed': failed })}>
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
        <span className="font-semibold">{DAGAMA_COMPONENT_STATUS_LABEL[runState.status]}</span>
        {runState.instance > 1 && <span className="text-muted-foreground">round {runState.instance}</span>}
      </div>
      {showReason ? (
        <div className={cn('text-[10px]', failed ? 'text-destructive' : 'text-muted-foreground')}>
          <span className="font-mono">{runState.reason}</span>
          {runState.message ? ` — ${runState.message}` : null}
        </div>
      ) : null}
    </div>
  );
}

function OutputList({
  outputs,
  produced,
  onOpenArtifact,
}: {
  outputs: readonly string[];
  produced: ReadonlySet<string>;
  onOpenArtifact?: (name: string) => void;
}) {
  return (
    <div className="dagama-outputs">
      <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
        {produced.size > 0 ? 'Produced' : 'Produces'}
      </span>
      {outputs.map((output) =>
        produced.has(output) && onOpenArtifact !== undefined ? (
          <button key={output} type="button" className="dagama-output" onClick={() => onOpenArtifact(output)}>
            {output}
          </button>
        ) : (
          <span key={output} className="dagama-output">
            {output}
          </span>
        ),
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

function CheckList({
  checks,
  onChange,
}: {
  checks: readonly DaGamaCheck[];
  onChange: (checks: DaGamaCheck[]) => void;
}) {
  const update = (index: number, patch: Partial<DaGamaCheck>) =>
    onChange(checks.map((check, position) => (position === index ? { ...check, ...patch } : check)));

  return (
    <div className="flex flex-col gap-2">
      {checks.length === 0 && (
        <div className="text-muted-foreground text-[11px]">
          No checks configured — Verify will report <span className="font-semibold">skipped</span>.
        </div>
      )}
      {checks.map((check, index) => (
        <div key={index} className="flex items-end gap-1.5">
          <Field label="Name">
            <input
              value={check.name}
              placeholder="typecheck"
              aria-label={`Check ${index + 1} name`}
              onChange={(event) => update(index, { name: event.target.value })}
              className="w-24"
            />
          </Field>
          <Field label="Command">
            {/* argv, not a shell string: the runner execs it directly, so there
                is no shell to quote for and nothing to inject through. */}
            <input
              value={check.argv.join(' ')}
              placeholder="npm run typecheck"
              spellCheck={false}
              aria-label={`Check ${index + 1} command`}
              onChange={(event) => update(index, { argv: event.target.value.split(/\s+/).filter(Boolean) })}
              className="w-full font-mono text-[11px]"
            />
          </Field>
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-muted-foreground hover:text-destructive mb-0.5"
            onClick={() => onChange(checks.filter((_, position) => position !== index))}
            aria-label={`Remove check ${check.name || index + 1}`}
            title="Remove check"
          >
            <Trash2Icon />
          </Button>
        </div>
      ))}
      <Button
        variant="ghost"
        size="xs"
        className="self-start"
        onClick={() => {
          const used = new Set(checks.map((check) => check.name));
          let position = checks.length + 1;
          while (used.has(`check ${position}`)) position += 1;
          onChange([
            ...checks,
            {
              name: checks.length === 0 && !used.has('test') ? 'test' : `check ${position}`,
              argv: ['npm', 'test'],
            },
          ]);
        }}
      >
        <PlusIcon />
        Add check
      </Button>
      <div className="text-muted-foreground text-[10px]">
        Runs without a shell. The first word must be one of: {DAGAMA_CHECK_COMMANDS.slice(0, 8).join(', ')}…
      </div>
    </div>
  );
}

function PublishFields({
  publish,
  onChange,
}: {
  publish: DaGamaPublishConfig;
  onChange: (patch: Partial<DaGamaPublishConfig>) => void;
}) {
  return (
    <div className="flex flex-col gap-2">
      <Field label="Base branch">
        <input
          value={publish.base}
          placeholder="checked-out branch of the selected project"
          spellCheck={false}
          aria-label="Base branch"
          onChange={(event) => onChange({ base: event.target.value })}
          className="w-full font-mono text-[11px]"
        />
      </Field>
      <label className="flex items-center gap-2 text-xs">
        <input
          type="checkbox"
          checked={publish.draft}
          onChange={(event) => onChange({ draft: event.target.checked })}
        />
        Open as a draft pull request
      </label>
      <div className="text-muted-foreground text-[10px]">
        Leave empty to use this project folder&apos;s current branch, including linked worktrees. Requires
        your approval, and opens at most one pull request per run.
      </div>
    </div>
  );
}

function SeatFields({ seat, onChange }: { seat: DaGamaSeat; onChange: (seat: DaGamaSeat) => void }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="grid grid-cols-2 gap-2">
        <Select
          label="Agent"
          value={seat.vendor}
          options={['claude', 'codex']}
          labels={VENDOR_LABELS}
          // A vendor switch invalidates the whole vocabulary, so the seat is
          // replaced with that vendor's complete default rather than left in a
          // transient tuple the server would refuse.
          onChange={(vendor) => onChange(defaultSeatForVendor(vendor as DaGamaVendor))}
        />
        <Select
          label="Model"
          value={seat.model}
          options={modelsFor(seat.vendor)}
          onChange={(model) => {
            const allowed = effortsFor(seat.vendor, model);
            onChange({
              ...seat,
              model,
              effort: allowed.includes(seat.effort) ? seat.effort : allowed[0],
            });
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
      {seat.permission === 'bypassPermissions' && (
        <div className="text-muted-foreground flex items-start gap-1.5 text-[10px]">
          <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
          <span>Claude has no sandbox — this grants a shell with your full permissions.</span>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Results and gates
// ---------------------------------------------------------------------------

type VerificationCheck = { name: string; exitCode: number; durationMs: number };
type VerificationDocument = {
  verdict: 'passed' | 'failed' | 'skipped';
  changeRevision: number;
  checks: VerificationCheck[];
};

export function VerifyResultsStrip({ onRead }: { onRead: () => Promise<string> }) {
  const [document, setDocument] = useState<VerificationDocument | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    setDocument(null);
    setError('');
    void (async () => {
      try {
        const parsed = JSON.parse(await onRead()) as VerificationDocument;
        if (active) setDocument(parsed);
      } catch (caught) {
        if (active) setError(caught instanceof Error ? caught.message : 'The verification is unavailable.');
      }
    })();
    return () => {
      active = false;
    };
  }, [onRead]);

  if (error !== '') return <div className="dagama-error">{error}</div>;
  if (document === null) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-[10px]">
        <LoaderCircleIcon className="size-3 animate-spin" />
        Loading checks…
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-muted-foreground flex flex-wrap items-center gap-x-2 text-[10px]">
        <span
          className={cn('font-semibold tracking-widest uppercase', {
            'text-success': document.verdict === 'passed',
            'text-destructive': document.verdict === 'failed',
          })}
        >
          {document.verdict}
        </span>
        <span>rev {document.changeRevision}</span>
      </div>
      {document.checks.length === 0 ? (
        <div className="text-muted-foreground text-[10px]">No checks configured</div>
      ) : (
        <div className="flex flex-col gap-1">
          {document.checks.map((check) => (
            <div
              key={check.name}
              className="flex items-center justify-between gap-2 rounded border px-2 py-1 text-[10px]"
            >
              <span className="truncate font-mono">{check.name}</span>
              <span className="text-muted-foreground shrink-0 font-mono">
                exit {check.exitCode} · {check.durationMs}ms
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export type GateDecider = (
  decision: 'approved' | 'rejected',
  options?: { publish?: boolean },
) => Promise<void>;

function StaleGateNotice({ gate }: { gate: DaGamaGateView }) {
  return (
    <div role="alert" className="text-warning flex items-start gap-1.5 text-[10px]">
      <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
      <span>{gate.staleReason}</span>
    </div>
  );
}

export function PublishGatePane({
  gate,
  publication,
  onReadPreflight,
  onDecide,
}: {
  gate: DaGamaGateView;
  publication: DaGamaPublicationRecord | null;
  onReadPreflight: () => Promise<DaGamaPublishPreflight>;
  onDecide: GateDecider;
}) {
  const [preflight, setPreflight] = useState<DaGamaPublishPreflight | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const open = gate.open;
  useEffect(() => {
    let active = true;
    if (!open) {
      setPreflight(null);
      return;
    }
    setError('');
    void (async () => {
      try {
        const result = await onReadPreflight();
        if (active) setPreflight(result);
      } catch (caught) {
        if (active)
          setError(caught instanceof Error ? caught.message : 'The publish preflight is unavailable.');
      }
    })();
    return () => {
      active = false;
    };
  }, [open, onReadPreflight]);

  async function decide(decision: 'approved' | 'rejected', options?: { publish?: boolean }) {
    setBusy(true);
    setError('');
    try {
      await onDecide(decision, options);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The gate decision failed.');
    } finally {
      setBusy(false);
    }
  }

  if (publication !== null) {
    return (
      <div className="dagama-panel text-[11px]">
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
    <div className="dagama-panel">
      <div className="text-[11px] font-semibold">Awaiting your approval</div>
      {!gate.decidable && gate.staleReason !== '' && <StaleGateNotice gate={gate} />}
      {preflight === null && error === '' ? (
        <div className="text-muted-foreground flex items-center gap-2 text-[11px]">
          <LoaderCircleIcon className="size-3.5 animate-spin" />
          Checking preflight…
        </div>
      ) : null}
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
          type="button"
          size="xs"
          variant="ghost"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('rejected')}
        >
          Reject
        </Button>
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            type="button"
            size="xs"
            variant="outline"
            disabled={busy || !gate.decidable}
            title="Mark the run done without commit, push, or pull request"
            onClick={() => void decide('approved', { publish: false })}
          >
            Approve without publish
          </Button>
          <Button
            type="button"
            size="xs"
            disabled={busy || !gate.decidable || preflight?.ok !== true}
            onClick={() => void decide('approved', { publish: true })}
          >
            {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
            Approve &amp; publish
          </Button>
        </div>
      </div>
      {error !== '' && (
        <div role="alert" className="dagama-error">
          {error}
        </div>
      )}
    </div>
  );
}

export function RepairGatePane({
  label,
  gate,
  onDecide,
}: {
  label: string;
  gate: DaGamaGateView;
  onDecide: GateDecider;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  if (!gate.open || gate.reason !== 'waiting_for_repair') return null;

  async function decide(decision: 'approved' | 'rejected') {
    setBusy(true);
    setError('');
    try {
      await onDecide(decision);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'The gate decision failed.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="dagama-panel">
      <div className="text-[11px] font-semibold">Repair rounds exhausted</div>
      <div className="text-muted-foreground text-[11px]">
        {label} opened a human gate after the automatic Build repair bound. Reject ends the run, or authorize
        one more Build round.
      </div>
      {!gate.decidable && gate.staleReason !== '' && <StaleGateNotice gate={gate} />}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button
          type="button"
          size="xs"
          variant="ghost"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('rejected')}
        >
          Reject run
        </Button>
        <Button
          type="button"
          size="xs"
          disabled={busy || !gate.decidable}
          onClick={() => void decide('approved')}
        >
          {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
          Retry Build
        </Button>
      </div>
      {error !== '' && (
        <div role="alert" className="dagama-error">
          {error}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Cards
// ---------------------------------------------------------------------------

export type ComponentCardProps = {
  component: DaGamaComponent;
  runState: DaGamaComponentRunState | null;
  gate: DaGamaGateView;
  publication: DaGamaPublicationRecord | null;
  editable: boolean;
  onChange: (patch: Partial<DaGamaComponent>) => void;
  onOpenArtifact?: (name: string) => void;
  onReadArtifact?: (name: string) => Promise<string>;
  onReadPreflight?: () => Promise<DaGamaPublishPreflight>;
  onDecideGate?: GateDecider;
};

/** Compact card body for the deterministic stages: Intake, Verify, Publish. */
export function ComponentCard({
  component,
  runState,
  gate,
  publication,
  editable,
  onChange,
  onOpenArtifact,
  onReadArtifact,
  onReadPreflight,
  onDecideGate,
}: ComponentCardProps) {
  const meta = DAGAMA_COMPONENT_META[component.id];
  const produced = new Set(runState?.outputs ?? []);
  const showPublishGate =
    component.id === 'publish' &&
    onDecideGate !== undefined &&
    onReadPreflight !== undefined &&
    (gate.open || publication !== null);
  const showRepairGate =
    component.id === 'verify' &&
    onDecideGate !== undefined &&
    gate.open &&
    gate.reason === 'waiting_for_repair';
  const showConfiguration = editable && !showPublishGate && !showRepairGate;
  const showVerification =
    component.id === 'verify' && produced.has('verification.json') && onReadArtifact !== undefined;

  return (
    <div className="dagama-card" onClick={stopClick}>
      <div className="dagama-purpose">{meta.purpose}</div>
      {runState && <RunStateStrip runState={runState} />}

      {component.id === 'verify' && showConfiguration && (
        <CheckList checks={component.checks} onChange={(checks) => onChange({ checks })} />
      )}

      {showVerification && <VerifyResultsStrip onRead={() => onReadArtifact('verification.json')} />}

      {showPublishGate && (
        <PublishGatePane
          gate={gate}
          publication={publication}
          onReadPreflight={onReadPreflight}
          onDecide={onDecideGate}
        />
      )}

      {showRepairGate && <RepairGatePane label="Verify" gate={gate} onDecide={onDecideGate} />}

      {component.publish && showConfiguration && (
        <PublishFields
          publish={component.publish}
          onChange={(patch) =>
            onChange({ publish: { ...(component.publish as DaGamaPublishConfig), ...patch } })
          }
        />
      )}

      <OutputList outputs={meta.outputs} produced={produced} onOpenArtifact={onOpenArtifact} />
    </div>
  );
}

export type SeatInfoPaneProps = {
  component: DaGamaComponent;
  runState: DaGamaComponentRunState | null;
  gate: DaGamaGateView;
  editable: boolean;
  onChange: (patch: Partial<DaGamaComponent>) => void;
  onOpenArtifact?: (name: string) => void;
  onDecideGate?: GateDecider;
};

/** Info companion: seat configuration, run status, artifacts, and the repair gate. */
export function SeatInfoPane({
  component,
  runState,
  gate,
  editable,
  onChange,
  onOpenArtifact,
  onDecideGate,
}: SeatInfoPaneProps) {
  const meta = DAGAMA_COMPONENT_META[component.id];
  const produced = new Set(runState?.outputs ?? []);
  const showRepairGate =
    component.id === 'review' &&
    onDecideGate !== undefined &&
    gate.open &&
    gate.reason === 'waiting_for_repair';
  const showConfiguration = editable && !showRepairGate;

  return (
    <div className="dagama-card" onClick={stopClick}>
      <div className="dagama-purpose">{meta.purpose}</div>
      {runState && <RunStateStrip runState={runState} />}
      {showConfiguration && component.seat && (
        <SeatFields seat={component.seat} onChange={(seat) => onChange({ seat })} />
      )}
      {!showConfiguration && component.seat && (
        <div className="text-muted-foreground text-[10px]">
          {VENDOR_LABELS[component.seat.vendor]} · {component.seat.model} · {component.seat.effort}
        </div>
      )}
      {showRepairGate && <RepairGatePane label="Review" gate={gate} onDecide={onDecideGate} />}
      <OutputList outputs={meta.outputs} produced={produced} onOpenArtifact={onOpenArtifact} />
    </div>
  );
}

export type SeatPromptPaneProps = {
  componentId: DaGamaSeatComponentId;
  boardPrompt: string;
  draft: string;
  /** True while the board is editable, i.e. this stage has not started. */
  editable: boolean;
  controls: DaGamaSeatControls;
  hasAttempt: boolean;
  onBoardPromptChange: (prompt: string) => void;
  onDraftChange: (draft: string) => void;
  onSend: (text: string) => Promise<void>;
  onReadPrompt?: () => Promise<string>;
};

/**
 * Prompt companion: the board's steering prompt while the stage is idle, and a
 * compose box that writes into the live terminal once the operator has taken
 * control of it.
 */
export function SeatPromptPane({
  componentId,
  boardPrompt,
  draft,
  editable,
  controls,
  hasAttempt,
  onBoardPromptChange,
  onDraftChange,
  onSend,
  onReadPrompt,
}: SeatPromptPaneProps) {
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [assembled, setAssembled] = useState<
    { state: 'loading' } | { state: 'ready' | 'error'; text: string } | null
  >(null);
  const title = SEAT_TITLES[componentId];

  async function send() {
    const text = draft.trim();
    if (!controls.canSend || text === '' || sending) return;
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

  async function openAssembled() {
    if (onReadPrompt === undefined) return;
    setAssembled({ state: 'loading' });
    try {
      setAssembled({ state: 'ready', text: await onReadPrompt() });
    } catch (caught) {
      setAssembled({
        state: 'error',
        text: caught instanceof Error ? caught.message : 'The assembled prompt is unavailable.',
      });
    }
  }

  if (editable) {
    return (
      <div className="dagama-prompt" onClick={stopClick}>
        <span className="text-muted-foreground text-[10px] font-semibold tracking-widest uppercase">
          Prompt card
        </span>
        <textarea
          value={boardPrompt}
          placeholder="Steer this stage — it cannot change what counts as done…"
          aria-label={`${title} prompt card`}
          onChange={(event) => onBoardPromptChange(event.target.value)}
        />
      </div>
    );
  }

  return (
    <div className="dagama-prompt" onClick={stopClick}>
      <textarea
        value={draft}
        aria-label={`${title} seat message`}
        placeholder={
          controls.canSend
            ? 'Message this seat — Enter to send, Shift+Enter for a newline'
            : controls.canTakeControl
              ? 'Take control to send into this terminal…'
              : 'Compose a message for this seat…'
        }
        onChange={(event) => onDraftChange(event.target.value)}
        onKeyDown={(event: ReactKeyboardEvent<HTMLTextAreaElement>) => {
          if (controls.canSend && event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void send();
          }
        }}
      />
      <div className="dagama-prompt-footer">
        <span className="text-muted-foreground">
          {error !== '' ? (
            <span className="text-destructive" role="alert">
              {error}
            </span>
          ) : controls.canSend ? null : controls.canTakeControl ? (
            'Automated turn — take control to send'
          ) : hasAttempt ? (
            'The terminal is not live'
          ) : (
            'Waiting for this seat'
          )}
        </span>
        <div className="flex items-center gap-1">
          {hasAttempt && onReadPrompt !== undefined && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              onClick={() => void openAssembled()}
              title="View the assembled prompt for this attempt"
            >
              <FileTextIcon />
              Assembled
            </Button>
          )}
          <Button
            type="button"
            size="xs"
            disabled={!controls.canSend || sending || draft.trim() === ''}
            onClick={() => void send()}
          >
            {sending ? <LoaderCircleIcon className="animate-spin" /> : <SendIcon />}
            Send
          </Button>
        </div>
      </div>
      {assembled !== null && (
        <pre className="bg-muted max-h-40 overflow-auto rounded p-2 text-[10px] whitespace-pre-wrap">
          {assembled.state === 'loading' ? 'Loading…' : assembled.text}
        </pre>
      )}
    </div>
  );
}

export type SeatTerminalPaneProps = {
  componentId: DaGamaSeatComponentId;
  runState: DaGamaComponentRunState | null;
  controls: DaGamaSeatControls;
  terminal: TerminalConnectionSnapshot | null;
  /** Message explaining why no terminal is attached, when one is expected. */
  attachError: string;
  visible: boolean;
  now: number;
  onInput: (data: string) => void;
  onResize: (cols: number, rows: number) => void;
  onReconnect: () => void;
  onRetry: () => Promise<void>;
  onCancel: () => Promise<void>;
  onTakeControl: () => Promise<void>;
  onHandBack: () => Promise<void>;
};

/**
 * The seat's live CLI.
 *
 * The transport is the guarded native PTY/WebSocket bridge, so there is no
 * iframe and no second unauthenticated port — the operator sees the same
 * terminal the controller drives, over the same authenticated origin.
 */
export function SeatTerminalPane({
  componentId,
  runState,
  controls,
  terminal,
  attachError,
  visible,
  now,
  onInput,
  onResize,
  onReconnect,
  onRetry,
  onCancel,
  onTakeControl,
  onHandBack,
}: SeatTerminalPaneProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const outputRef = useRef<HTMLPreElement>(null);
  const attempt = runState?.attempt ?? null;
  const title = SEAT_TITLES[componentId];

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

  async function run(action: () => Promise<void>, fallback: string) {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : fallback);
    } finally {
      setBusy(false);
    }
  }

  if (!visible) {
    return (
      <div className="dagama-terminal-empty">
        <TerminalIcon className="size-4" />
        <span>The live CLI opens here when this stage runs</span>
        <span className="text-[10px]">Configure the seat on the info card · prompt beside it</span>
      </div>
    );
  }

  const elapsed = elapsedLabel(
    attempt?.startedAt ?? runState?.startedAt ?? null,
    attempt?.finishedAt ?? null,
    now,
  );

  return (
    <div className="dagama-terminal" onClick={stopClick}>
      <div className="dagama-terminal-status">
        <span className="truncate">
          {title}
          {attempt ? ` · attempt ${attempt.attempt}` : null}
          {elapsed ? ` · ${elapsed}` : null}
          {attempt ? ` · ${attempt.ownership}` : null}
          {attempt?.exitCode != null ? ` · exit ${attempt.exitCode}` : null}
          {terminal ? ` · ${terminal.status}` : null}
        </span>
        <span className="dagama-terminal-actions">
          {controls.canTakeControl && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(onTakeControl, 'Takeover failed.')}
            >
              <UserRoundIcon />
              Take control
            </Button>
          )}
          {controls.canHandBack && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(onHandBack, 'Handback failed.')}
            >
              <HandIcon />
              Return
            </Button>
          )}
          {controls.canCancel && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(onCancel, 'Cancel failed.')}
            >
              <SquareIcon />
              Cancel
            </Button>
          )}
          {controls.canRetry && (
            <Button
              type="button"
              size="xs"
              variant="ghost"
              disabled={busy}
              onClick={() => void run(onRetry, 'Retry failed.')}
            >
              <RotateCcwIcon />
              Retry
            </Button>
          )}
          {terminal !== null && (terminal.status === 'error' || terminal.status === 'closed') && (
            <Button type="button" size="xs" variant="ghost" onClick={onReconnect}>
              <RefreshCwIcon />
              Reconnect
            </Button>
          )}
        </span>
      </div>
      <pre
        ref={outputRef}
        role="log"
        aria-label={`${title} seat terminal`}
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
            : attempt?.status === 'exited'
              ? 'This terminal session ended.'
              : 'Connecting to the seat terminal…')}
      </pre>
      {error !== '' && (
        <div role="alert" className="dagama-error border-t px-2 py-1">
          {error}
        </div>
      )}
    </div>
  );
}
