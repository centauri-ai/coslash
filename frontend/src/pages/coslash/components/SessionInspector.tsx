import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  CheckIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  EyeIcon,
  LoaderCircleIcon,
  PlayIcon,
  SquareCheckIcon,
  SquareIcon,
  TerminalIcon,
  XIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogTrigger } from '@/components/ui/dialog';
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { CopyableBadge } from '@/pages/coslash/components/CopyableBadge';
import { DiffList } from '@/pages/coslash/components/DiffList';
import { MachineBadge } from '@/pages/coslash/components/MachineBadge';
import {
  SessionId,
  SessionName,
  SessionVendorBadge,
  SubagentDialogContent,
  SubagentModelBadge,
  TokenBreakdown,
} from '@/pages/coslash/components/SessionCard';
import { SnapshotPreviewDialog } from '@/pages/coslash/components/SnapshotPreviewDialog';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { useLaunchTerminal } from '@/pages/coslash/hooks/use-launch-terminal';
import { synthesisRequestPath, useFileDiff, type FileSelection } from '@/pages/coslash/hooks/use-sessions';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import {
  blocksFromTexts,
  collapseDebriefBlocks,
  parseDebriefText,
  type DebriefBlock,
} from '@/pages/coslash/lib/debrief-text';
import {
  digestDateKey,
  formatDigestDateDivider,
  formatDigestDateRange,
  formatDigestTime,
  formatDuration,
  formatEstimatedCost,
  formatTimeAgo,
  formatTokens,
} from '@/pages/coslash/lib/format';
import { handoffBrief } from '@/pages/coslash/lib/handoff';
import { type MachineFact } from '@/pages/coslash/lib/machines';
import { teamPreviewEnabled } from '@/pages/coslash/lib/preview';
import {
  boardStatusKey,
  displayStatusLabel,
  environmentFact,
  getModality,
  getSessionOutcome,
  getVendor,
  goalSourceLabel,
  isLocalSession,
  resolveGoal,
  resumeDisabled,
  resumeDisabledHint,
  sessionKey,
  STATUSES,
  SUBAGENT_STATUSES,
  type DigestEntry,
  type Session,
  type SessionDetail,
  type SessionIdentity,
} from '@/pages/coslash/lib/session';
import { HOUR, MINUTE } from '@/pages/coslash/lib/time';

type SynthesisResponse = {
  synthesis: SessionDetail['synthesis'];
  synthesisPending: boolean;
  synthesisError?: string;
};

/* oxlint-disable react/only-export-components -- exported for focused rendering tests */
export function filePanelOpen(selection: FileSelection | null, session: SessionIdentity | null): boolean {
  return (
    selection != null &&
    session != null &&
    selection.sourceId === session.sourceId &&
    selection.agent === session.agent &&
    selection.sessionId === session.id
  );
}
/* oxlint-enable react/only-export-components */

function useSessionDetail(
  session: Session | null,
  sessionsVersion: number,
  synthesisSettingsKey: string,
): { detail: SessionDetail | null; loadError: string | null } {
  const [loaded, setLoaded] = useState<({ key: string } & SynthesisResponse) | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const pollDeadline = useRef<{ key: string; deadline: number } | null>(null);
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const key = session == null ? null : sessionKey(session);

  useEffect(() => {
    const current = sessionRef.current;
    setLoaded((prev) => (prev?.key === key ? prev : null));
    setLoadError(null);
    if (current == null || key == null) {
      pollDeadline.current = null;
      return;
    }
    if (!isLocalSession(current)) {
      pollDeadline.current = null;
      return;
    }
    if (pollDeadline.current?.key !== key) {
      pollDeadline.current = null;
    }
    const identity = {
      sourceId: current.sourceId,
      agent: current.agent,
      id: current.id,
    };
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const res = await apiFetch(synthesisRequestPath(identity), {
          signal: controller.signal,
        });
        if (!res.ok) return;
        const result = (await res.json()) as SynthesisResponse;
        if (result.synthesis == null && result.synthesisPending) {
          pollDeadline.current ??= { key, deadline: Date.now() + 2 * MINUTE };
          if (Date.now() < pollDeadline.current.deadline) {
            timer = setTimeout(load, 3_000);
            setLoaded({ key, ...result });
          } else {
            setLoaded({ key, ...result, synthesisPending: false });
          }
        } else {
          pollDeadline.current = null;
          setLoaded({ key, ...result });
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted && error instanceof ApiAuthenticationError) {
          setLoadError(error.message);
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer != null) clearTimeout(timer);
    };
  }, [key, sessionsVersion, synthesisSettingsKey]);

  if (session == null) return { detail: null, loadError };
  if (loaded?.key !== sessionKey(session)) return { detail: session, loadError };
  return {
    detail: {
      ...session,
      synthesis: loaded.synthesis,
      synthesisPending: loaded.synthesisPending,
      synthesisError: loaded.synthesisError,
    },
    loadError,
  };
}

function contextFillReadiness(detail: SessionDetail): { value: string; tone: string } | null {
  if (detail.contextTokens == null) return null;
  if (detail.contextWindow == null) {
    return {
      value: `${formatTokens(detail.contextTokens)} used - window not available`,
      tone: 'text-muted-foreground',
    };
  }
  const pct = Math.round((detail.contextTokens / detail.contextWindow) * 100);
  return { value: `${pct}% of ${formatTokens(detail.contextWindow)}`, tone: fillTone(pct) };
}

function branchDriftReadiness(git: SessionDetail['git']): { value: string; tone: string } | null {
  if (git == null) return null;
  return {
    value: `${git.ahead} ahead, ${git.behind} behind ${git.baseBranch}.`,
    tone: driftTone(git.behind),
  };
}

function treeStaleReadiness(lastEditAt: number | null): { value: string; tone: string } | null {
  if (lastEditAt == null) return null;
  return { value: `Last edit ${formatTimeAgo(lastEditAt)}.`, tone: staleTone(Date.now() - lastEditAt) };
}

function fillTone(pct: number): string {
  if (pct >= 85) return 'text-destructive';
  if (pct >= 65) return 'text-warning-fg';
  return 'text-success-fg';
}

function driftTone(behind: number): string {
  if (behind > 15) return 'text-destructive';
  if (behind > 5) return 'text-warning-fg';
  return 'text-success-fg';
}

function staleTone(ageMs: number): string {
  if (ageMs > 72 * HOUR) return 'text-destructive';
  if (ageMs > 3 * HOUR) return 'text-warning-fg';
  return 'text-success-fg';
}

function SectionLabel({ title, note }: { title: string; note?: string }) {
  return (
    <div className="flex items-baseline gap-2 pb-2">
      <span className="text-brand text-xs font-bold tracking-widest">{title}</span>
      {note && <span className="text-muted-foreground text-xs">{note}</span>}
    </div>
  );
}

function FieldLabel({ children }: { children: string }) {
  return <div className="text-muted-foreground pb-1 text-xs font-semibold tracking-wide">{children}</div>;
}

export function SessionModelUsage({
  agent,
  model,
  tokens,
}: Pick<SessionDetail, 'agent' | 'model' | 'tokens'>) {
  const vendor = getVendor(agent);
  if (model == null) return <span className={cn('font-bold', vendor.fg)}>unknown model</span>;

  const otherModels = Object.keys(tokens).filter((tokenModel) => tokenModel !== model);
  if (otherModels.length === 0) return <span className={cn('font-bold', vendor.fg)}>{model}</span>;

  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex items-center gap-1">
        {otherModels.map((otherModel) => (
          <span key={otherModel} aria-hidden="true" className="size-2 rounded-full bg-neutral-200" />
        ))}
        <span aria-hidden="true" className={cn('size-2 rounded-full bg-current', vendor.fg)} />
      </span>
      <span className={cn('font-bold', vendor.fg)}>{model}</span>
      <span className="text-muted-foreground">
        {otherModels.length === 1 ? `after ${otherModels[0]}` : `with ${otherModels.length} other models`}
      </span>
    </span>
  );
}

function HeaderMeta({ detail, showMachineBadge }: { detail: SessionDetail; showMachineBadge: boolean }) {
  const status = STATUSES[boardStatusKey(detail)];

  return (
    <div className="flex flex-col gap-2 pt-2">
      <div className="text-muted-foreground flex flex-wrap items-center justify-between gap-x-2 gap-y-1 font-mono text-xs">
        <div className="flex min-w-40 flex-1 items-center gap-1 overflow-hidden">
          <Badge variant="secondary">{environmentFact(detail.repo)}</Badge>
          <span>/</span>
          {detail.branch == null || detail.branch.trim() === '' ? (
            <Badge variant="secondary">—</Badge>
          ) : (
            <CopyableBadge
              value={detail.branch}
              ariaLabel={`Copy branch ${detail.branch}`}
              copiedLabel="Branch name copied"
              className="shrink"
            >
              <span className="truncate">{detail.branch}</span>
            </CopyableBadge>
          )}
          {showMachineBadge && <MachineBadge label={detail.sourceLabel} />}
        </div>
        <span
          className={cn(
            'flex shrink-0 items-center gap-1 font-sans font-semibold whitespace-nowrap',
            status.fg,
          )}
        >
          {!detail.displayStale && <span className={cn('size-2 rounded-full', status.dot)} />}
          {displayStatusLabel(detail)} · {getModality(detail.entrypoint)}
        </span>
      </div>
      <div className="bg-muted rounded-lg border p-2 font-mono text-xs">
        <div className="flex flex-wrap items-baseline justify-between gap-1">
          <SessionModelUsage agent={detail.agent} model={detail.model} tokens={detail.tokens} />
          <span className="font-bold">
            <UnpricedModelWarning unpriced={detail.unpricedModels}>
              {formatEstimatedCost(detail.cost)}
            </UnpricedModelWarning>
          </span>
        </div>
        <div className="text-muted-foreground pt-1">
          {formatDuration(detail.durationMs)} · {detail.turns} turns · {detail.toolUses} tools ·{' '}
          {detail.errors} errors
        </div>
        <TokenBreakdown tokens={detail.tokens} />
      </div>
    </div>
  );
}

function ReadinessCell({ label, value, tone }: { label: string; value?: string; tone?: string }) {
  return (
    <div className="bg-background p-2">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className={cn('pt-1 text-xs font-semibold', tone)}>{value ?? '—'}</div>
    </div>
  );
}

function CacheWindowMark({ within, label }: { within: boolean; label: string }) {
  return (
    <span
      className={cn('flex items-center gap-1 text-xs', within ? 'text-success-fg' : 'text-muted-foreground')}
    >
      {within ? <CheckIcon className="size-3 shrink-0" /> : <XIcon className="size-3 shrink-0" />}
      {label}
    </span>
  );
}

// Cache TTL refreshes on every request, so warmth keys off the transcript's
// last write: within 5 min both windows hold, within 1 hr only the 1-hr one.
function PromptCacheCell({ lastAccessAt }: { lastAccessAt: number }) {
  const ageMin = (Date.now() - lastAccessAt) / MINUTE;
  const within5m = ageMin <= 5;
  const within1h = ageMin <= 60;

  return (
    <div className="bg-background p-2">
      <div className="text-muted-foreground text-xs">Prompt cache</div>
      <div className="flex flex-wrap items-baseline gap-1 pt-1">
        <span className={cn('text-xs font-semibold', within1h ? 'text-success-fg' : 'text-warning-fg')}>
          {within1h ? 'warm' : 'cold'}
        </span>
        <span className="text-muted-foreground text-xs">{formatTimeAgo(lastAccessAt)}</span>
      </div>
      <div className="flex flex-wrap gap-2 pt-1">
        <CacheWindowMark within={within5m} label="5 min" />
        <CacheWindowMark within={within1h} label="1 hr" />
      </div>
    </div>
  );
}

function LaunchError({ message }: { message: string | null }) {
  if (message == null) return null;
  return <span className="text-destructive text-xs">{message}</span>;
}

function DisabledLaunchTooltip({ hint, children }: { hint?: string; children: ReactNode }) {
  if (hint == null) return children;
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0}>{children}</span>
        </TooltipTrigger>
        <TooltipContent>{hint}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

function ResumeSessionButton({ detail, disabledHint }: { detail: SessionDetail; disabledHint?: string }) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const disabled = resumeDisabled(detail, disabledHint);

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={disabledHint}>
        <Button className="bg-brand w-fit p-2 text-xs" onClick={() => launch('resume')} disabled={disabled}>
          <PlayIcon />
          <span>Resume</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

function StartNewSessionButton({
  detail,
  brief,
  onCopy,
  disabledHint,
}: {
  detail: SessionDetail;
  brief: string;
  onCopy: () => void;
  disabledHint?: string;
}) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const disabled = !isLocalSession(detail) && (detail.displayStale || detail.launchable === false);

  const startNewSession = () => {
    onCopy();
    launch('new', brief);
  };

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={disabledHint}>
        <Button className="bg-brand w-fit p-2 text-xs" onClick={startNewSession} disabled={disabled}>
          <TerminalIcon />
          <span>Start fresh with handoff</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

function HandoffSection({
  detail,
  remoteLaunchable,
  remoteLaunchHint,
}: {
  detail: SessionDetail;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
}) {
  const [copied, setCopied] = useState(false);
  const contextFill = contextFillReadiness(detail);
  const branchDrift = branchDriftReadiness(detail.git);
  const treeStale = treeStaleReadiness(detail.lastEditAt);

  const brief = handoffBrief(detail);

  const copyBrief = () => {
    navigator.clipboard?.writeText(brief);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="flex flex-col gap-2">
      <SectionLabel title="RESUME OR HAND OFF" />
      <div className="bg-border grid grid-cols-5 gap-px overflow-hidden rounded-lg border">
        <ReadinessCell label="Context used" value={contextFill?.value} tone={contextFill?.tone} />
        <ReadinessCell label="Compactions" value={String(detail.compactions)} />
        <ReadinessCell label="Branch" value={branchDrift?.value} tone={branchDrift?.tone} />
        <ReadinessCell label="Working tree" value={treeStale?.value} tone={treeStale?.tone} />
        <PromptCacheCell lastAccessAt={detail.mtime} />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <StartNewSessionButton
          detail={detail}
          brief={brief}
          onCopy={copyBrief}
          disabledHint={!isLocalSession(detail) && !remoteLaunchable ? remoteLaunchHint : undefined}
        />
        <Button variant="outline" className="w-fit p-2 text-xs" onClick={copyBrief}>
          <span>Copy handoff</span>
        </Button>
        {copied && <span className="text-xs text-neutral-300">copied to clipboard</span>}
      </div>
      {!isLocalSession(detail) && (
        <div className="text-muted-foreground text-xs">
          {remoteLaunchable
            ? 'Remote terminal actions open through SSH. File diffs, synthesis, Hub sharing, and command history remain local-only.'
            : 'Remote terminal actions are available when SSH reconnects. File diffs, synthesis, Hub sharing, and command history remain local-only.'}
        </div>
      )}
    </div>
  );
}

function RecapSection({ detail }: { detail: SessionDetail }) {
  const goal = resolveGoal(detail);
  const synthesizing = detail.synthesis == null && detail.synthesisPending;
  const synthesisPlaceholder = (
    <div className="text-muted-foreground flex items-center gap-1 pt-1 text-xs">
      <LoaderCircleIcon className="size-3 animate-spin" />
      <span>Synthesizing…</span>
    </div>
  );
  const outcome = getSessionOutcome(detail);

  return (
    <div>
      <SectionLabel title="DEBRIEF" />
      <div className="rounded-lg border p-3">
        {detail.synthesisError && (
          <div role="alert" className="bg-warning-bg text-warning-fg rounded-lg p-2 text-xs">
            {detail.synthesisError} Using the transcript-derived debrief instead.
          </div>
        )}
        <div className={cn('flex items-center gap-2', { 'pt-3': detail.synthesisError != null })}>
          <span className="text-muted-foreground text-xs">GOAL</span>
          <Badge variant="secondary" className="text-xs">
            {goalSourceLabel(goal.source)}
          </Badge>
        </div>
        <div className="pt-2">
          <DebriefProse key={`${sessionKey(detail)}:goal`} blocks={blocksFromTexts(goal.texts)} tone="goal" />
        </div>
        <div className="border-b pt-3" />
        <div className="text-muted-foreground pt-3 text-xs">OUTCOME</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : (
          <div className="pt-1">
            <DebriefProse
              key={`${sessionKey(detail)}:outcome`}
              blocks={outcome ? parseDebriefText(outcome) : []}
              empty="—"
              tone="outcome"
            />
          </div>
        )}
        <div className="text-muted-foreground pt-3 text-xs">KEY DECISIONS</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : detail.synthesis?.keyDecisions.length ? (
          <div className="flex flex-col gap-1 pt-1">
            {detail.synthesis.keyDecisions.map((decision) => (
              <div key={decision} className="flex items-start gap-2 text-xs">
                <span className="bg-muted-foreground mt-1 size-1 shrink-0 rounded-full" />
                <span>{decision}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="pt-1 text-xs">—</div>
        )}
      </div>
    </div>
  );
}

const DEBRIEF_PREVIEW_UNITS = 3;

function DebriefProse({
  blocks,
  empty = '—',
  tone,
}: {
  blocks: DebriefBlock[];
  empty?: string;
  tone: 'goal' | 'outcome';
}) {
  const [expanded, setExpanded] = useState(false);
  if (blocks.length === 0) return <div className="text-xs">{empty}</div>;

  const preview = collapseDebriefBlocks(blocks, DEBRIEF_PREVIEW_UNITS);
  const canExpand = preview.truncated;
  const shown = expanded ? blocks : preview.blocks;

  return (
    <div className="flex flex-col gap-2">
      <div
        key={expanded ? 'full' : 'preview'}
        className={cn('border-border border-l-2 pl-3', {
          'border-l-brand/40': tone === 'goal',
          'border-l-recap/40': tone === 'outcome',
        })}
      >
        <DebriefBlocks blocks={shown} tone={tone} />
      </div>
      {canExpand && (
        <div
          className="text-brand flex w-fit cursor-pointer items-center gap-1 text-xs select-none"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
          <span>
            {expanded
              ? 'show less'
              : preview.hiddenCount > 0
                ? `show full · ${preview.hiddenCount} more`
                : 'show full'}
          </span>
        </div>
      )}
    </div>
  );
}

function DebriefBlocks({ blocks, tone }: { blocks: DebriefBlock[]; tone: 'goal' | 'outcome' }) {
  return (
    <div className="flex flex-col gap-2">
      {blocks.map((block, index) => {
        if (block.kind === 'heading') {
          return (
            <div key={`h-${index}`} className="text-xs font-semibold tracking-wide">
              {block.text}
            </div>
          );
        }
        if (block.kind === 'list') {
          if (block.ordered) {
            return (
              <ol
                key={`l-${index}`}
                className={cn('list-decimal space-y-1.5 pl-4 text-xs', {
                  italic: tone === 'goal',
                })}
              >
                {block.items.map((item, itemIndex) => (
                  <li key={`${index}-${itemIndex}`} className="leading-relaxed">
                    {item}
                  </li>
                ))}
              </ol>
            );
          }
          return (
            <ul
              key={`l-${index}`}
              className={cn('flex list-none flex-col gap-1.5 text-xs', {
                italic: tone === 'goal',
              })}
            >
              {block.items.map((item, itemIndex) => (
                <li key={`${index}-${itemIndex}`} className="flex items-start gap-2">
                  <span className="bg-muted-foreground mt-1.5 size-1 shrink-0 rounded-full" />
                  <span className="min-w-0 flex-1 leading-relaxed">{item}</span>
                </li>
              ))}
            </ul>
          );
        }
        return (
          <p
            key={`p-${index}`}
            className={cn('text-xs leading-relaxed', {
              italic: tone === 'goal',
            })}
          >
            {block.text}
          </p>
        );
      })}
    </div>
  );
}

type DigestCategory = DigestEntry['category'];

const DEFAULT_HIDDEN_CATEGORIES: DigestCategory[] = [];

const DIGEST_CATEGORIES: Record<DigestCategory, { label: string; fg: string; dot: string }> = {
  first_prompt: { label: 'FIRST PROMPT', fg: 'text-success-fg', dot: 'bg-success-fg' },
  question: { label: 'QUESTION', fg: 'text-question', dot: 'bg-question' },
  subagent: { label: 'Subagent', fg: 'text-subagent', dot: 'bg-subagent' },
  todos: { label: 'TODOS', fg: 'text-muted-foreground', dot: 'bg-muted-foreground' },
  recap: { label: 'RECAP', fg: 'text-recap', dot: 'bg-recap' },
  user: { label: 'USER TURN', fg: 'text-brand', dot: 'bg-brand' },
  compaction: { label: 'COMPACTION', fg: 'text-compaction', dot: 'bg-compaction' },
};

function CategoryChip({
  category,
  count,
  active,
  onToggle,
}: {
  category: DigestCategory;
  count: number;
  active: boolean;
  onToggle: () => void;
}) {
  const meta = DIGEST_CATEGORIES[category];
  return (
    <div
      className={cn('flex cursor-pointer items-center gap-1 rounded-full border px-2 py-1 select-none', {
        'bg-muted': active,
        'border-dashed': !active,
      })}
      onClick={onToggle}
    >
      <span className={cn('size-2 rounded-full', active ? meta.dot : 'bg-neutral-300')} />
      <span
        className={cn('text-xs font-semibold', {
          'text-muted-foreground': !active,
        })}
      >
        {meta.label}
      </span>
      <span className="text-muted-foreground text-xs">{count}</span>
    </div>
  );
}

function DigestRow({ entry, endsDay }: { entry: DigestEntry; endsDay?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const meta = DIGEST_CATEGORIES[entry.category];
  const collapsible = entry.category === 'recap' && entry.description.length > 120;

  return (
    <div className={cn('border-border flex items-baseline gap-2 py-1', { 'border-b': !endsDay })}>
      <span className={cn('w-24 shrink-0 text-xs font-bold tracking-wide', meta.fg)}>{meta.label}</span>
      <div className="min-w-0 flex-1">
        <div className={cn('text-xs', { 'line-clamp-1': collapsible && !expanded })}>{entry.description}</div>
        {entry.answer != null && (
          <div className="border-border text-muted-foreground border-l-2 pl-2 text-xs">{entry.answer}</div>
        )}
        {collapsible && (
          <div
            className="text-brand flex cursor-pointer items-center gap-1 pt-1 text-xs"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
            <span>{expanded ? 'collapse' : 'expand full recap'}</span>
          </div>
        )}
      </div>
      <span className="text-muted-foreground flex shrink-0 flex-col items-end font-mono text-xs whitespace-nowrap">
        <span>turn {entry.turn}</span>
        {entry.time != null && entry.time > 0 && (
          <span className="text-muted-foreground">{formatDigestTime(entry.time)}</span>
        )}
      </span>
    </div>
  );
}

function SubagentDigestRow({ subagentId, detail }: { subagentId: string; detail: SessionDetail }) {
  const subagent = detail.subagents.find((candidate) => candidate.id === subagentId);
  if (!subagent) {
    throw new Error(`digest references subagent ${subagentId}, which is not on the session`);
  }
  const parentName = detail.name;
  const status = SUBAGENT_STATUSES[subagent.status].label;
  return (
    <Dialog>
      <DialogTrigger asChild>
        <div className="bg-subagent-card flex cursor-pointer items-baseline gap-2 rounded-lg border p-2">
          <span className="text-subagent w-24 shrink-0 text-xs font-bold tracking-wide">Subagent</span>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="min-w-0 truncate text-sm font-semibold">{subagent.name}</span>
              <SubagentModelBadge model={subagent.model} />
            </div>
            <div className="text-muted-foreground truncate pt-1 text-xs">
              {subagent.result === '' ? status : `${status}: ${subagent.result}`}
            </div>
          </div>
          <div className="text-brand flex shrink-0 items-center gap-1 text-xs">
            <span>open</span>
            <ExternalLinkIcon className="size-3" />
          </div>
        </div>
      </DialogTrigger>
      <SubagentDialogContent subagent={subagent} parentName={parentName} />
    </Dialog>
  );
}

function DateDivider({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground grid grid-cols-[1fr_auto_1fr] items-center gap-2 pt-2 text-xs font-bold tracking-wide">
      <span className="bg-border h-px" />
      <span>{label}</span>
      <span className="bg-border h-px" />
    </div>
  );
}

function DigestSection({ detail }: { detail: SessionDetail }) {
  const [hiddenCategories, setHiddenCategories] = useState<Set<DigestCategory>>(
    () => new Set(DEFAULT_HIDDEN_CATEGORIES),
  );
  const digest = detail.digest;
  if (digest.length === 0) return null;

  const counts = new Map<DigestCategory, number>();
  for (const entry of digest) counts.set(entry.category, (counts.get(entry.category) ?? 0) + 1);

  const toggleCategory = (category: DigestCategory) => {
    setHiddenCategories((prev) => {
      const next = new Set(prev);
      if (next.has(category)) next.delete(category);
      else next.add(category);
      return next;
    });
  };

  const visible = digest.filter((entry) => !hiddenCategories.has(entry.category));
  const times = digest.map((e) => e.time ?? 0).filter((t) => t > 0);
  const dateRange = formatDigestDateRange(times);

  const endsDayIndices = new Set<number>();
  let lastDate = '';
  for (let i = 0; i < visible.length; i++) {
    const entryDate = visible[i].time != null && visible[i].time! > 0 ? digestDateKey(visible[i].time!) : '';
    if (i > 0 && entryDate !== '' && lastDate !== '' && entryDate !== lastDate) {
      endsDayIndices.add(i - 1);
    }
    if (entryDate !== '') {
      lastDate = entryDate;
    }
  }

  let previousDate = '';
  const rows = visible.map((entry, index) => {
    const entryDate = entry.time != null && entry.time > 0 ? digestDateKey(entry.time) : '';
    const showDivider = entryDate !== '' && entryDate !== previousDate;
    if (entryDate !== '') {
      previousDate = entryDate;
    }
    return (
      <div key={index}>
        {showDivider && <DateDivider label={formatDigestDateDivider(entry.time!)} />}
        {entry.category === 'subagent' ? (
          <SubagentDigestRow subagentId={entry.subagentId!} detail={detail} />
        ) : (
          <DigestRow entry={entry} endsDay={endsDayIndices.has(index) || index === visible.length - 1} />
        )}
      </div>
    );
  });

  return (
    <div>
      <div className="flex items-baseline justify-between gap-2 pb-2">
        <div className="flex items-baseline gap-2">
          <span className="text-muted-foreground text-xs font-semibold tracking-wide">TIMELINE</span>
          <span className="text-muted-foreground text-xs">key events from this session</span>
        </div>
        <span className="text-muted-foreground text-xs">{dateRange}</span>
      </div>
      <div className="flex flex-wrap gap-1 pb-2">
        {(Object.keys(DIGEST_CATEGORIES) as DigestCategory[])
          .filter((category) => counts.has(category))
          .map((category) => (
            <CategoryChip
              key={category}
              category={category}
              count={counts.get(category)!}
              active={!hiddenCategories.has(category)}
              onToggle={() => toggleCategory(category)}
            />
          ))}
      </div>
      <div className="flex flex-col gap-1">{rows}</div>
    </div>
  );
}

function StatCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="bg-background p-2">
      <div className="text-base font-bold">{value}</div>
      <div className="text-muted-foreground text-xs">{label}</div>
    </div>
  );
}

function ArtifactStats({ detail }: { detail: SessionDetail }) {
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  const cells: [string, string][] = [
    [String(detail.fileEdits.length), newFiles ? `files (${newFiles} new)` : 'files'],
    [String(detail.prs), 'PRs'],
    [String(detail.subagents.length), 'subagents'],
  ];

  return (
    <div>
      <SectionLabel title="ARTIFACTS" note="what this session produced" />
      <div className="bg-border grid grid-cols-3 gap-px overflow-hidden rounded-lg border">
        {cells.map(([value, label]) => (
          <StatCell key={label} value={value} label={label} />
        ))}
        <div className="bg-background" />
      </div>
    </div>
  );
}

function FilesChangedList({
  detail,
  onSelectFile,
}: {
  detail: SessionDetail;
  onSelectFile: ((path: string) => void) | null;
}) {
  if (detail.fileEdits.length === 0) return null;
  const newFiles = detail.fileEdits.filter((fileEdit) => fileEdit.isNew).length;
  return (
    <div>
      <FieldLabel>{`FILES CHANGED · ${detail.fileEdits.length} TOTAL${newFiles ? `, ${newFiles} NEW` : ''}`}</FieldLabel>
      <div className="rounded-sm border p-2">
        {detail.fileEdits.map((fileEdit) => (
          <div key={fileEdit.path} className="flex items-center justify-between gap-2 py-1 font-mono text-xs">
            {onSelectFile == null ? (
              <span
                title={fileEdit.path}
                className={cn('text-muted-foreground min-w-0 truncate text-left', {
                  'text-success-fg': fileEdit.isNew,
                })}
              >
                {fileEdit.path.split('/').pop()}
              </span>
            ) : (
              <button
                type="button"
                title={fileEdit.path}
                className={cn(
                  'text-muted-foreground min-w-0 cursor-pointer truncate text-left hover:underline',
                  {
                    'text-success-fg': fileEdit.isNew,
                  },
                )}
                onClick={() => onSelectFile(fileEdit.path)}
              >
                {fileEdit.path.split('/').pop()}
              </button>
            )}
            <span className="whitespace-nowrap">
              <span className="text-success-fg">+{fileEdit.adds}</span>{' '}
              <span className="text-destructive">−{fileEdit.dels}</span>{' '}
              <span className="text-muted-foreground">
                · {fileEdit.edits} {fileEdit.edits === 1 ? 'edit' : 'edits'}
              </span>
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}

function CommitsAndTodos({ detail }: { detail: SessionDetail }) {
  return (
    <div className="flex gap-4 pt-4">
      <div className="min-w-0 flex-1">
        <FieldLabel>COMMITS</FieldLabel>
        {detail.commits.length === 0 ? (
          <div className="text-muted-foreground text-xs">—</div>
        ) : (
          detail.commits.map((commit, index) => (
            <div key={index} className="truncate py-1 font-mono text-xs">
              {commit}
            </div>
          ))
        )}
      </div>
      <div className="min-w-0 flex-1">
        <FieldLabel>TODOS</FieldLabel>
        {detail.todos.length === 0 ? (
          <div className="text-muted-foreground text-xs">none</div>
        ) : (
          detail.todos.map((todo, index) => (
            <div
              key={index}
              className={cn('flex items-start gap-1 py-1 text-xs', {
                'text-muted-foreground': todo.done,
              })}
            >
              {todo.done ? (
                <SquareCheckIcon className="size-4 shrink-0" />
              ) : (
                <SquareIcon className="size-4 shrink-0" />
              )}
              <span className="min-w-0">{todo.text}</span>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function CommandsSection({ detail }: { detail: SessionDetail }) {
  const [open, setOpen] = useState(false);
  if (detail.commands.length === 0) return null;

  return (
    <div className="pt-4">
      <div className="flex cursor-pointer items-center gap-2" onClick={() => setOpen(!open)}>
        {open ? (
          <ChevronDownIcon className="text-muted-foreground size-3" />
        ) : (
          <ChevronRightIcon className="text-muted-foreground size-3" />
        )}
        <FieldLabel>{`${detail.commands.length} COMMANDS`}</FieldLabel>
        <div className="flex-1 border-b" />
      </div>
      {open && (
        <div className="max-h-44 overflow-auto rounded-lg bg-neutral-900 p-3">
          <pre className="font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap text-neutral-300">
            {detail.commands.join('\n')}
          </pre>
        </div>
      )}
    </div>
  );
}

function InspectorBody({
  detail,
  onSelectFile,
  remoteLaunchable,
  remoteLaunchHint,
}: {
  detail: SessionDetail;
  onSelectFile: ((path: string) => void) | null;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
}) {
  // scroll on the outer div, layout on the inner one — flex children of a
  // scroll container shrink to fit instead of overflowing, which collapses
  // the overflow-hidden stat grids
  return (
    <div className="flex-1 overflow-y-auto pb-2">
      <div className="flex flex-col gap-2 px-4">
        <HandoffSection
          detail={detail}
          remoteLaunchable={remoteLaunchable}
          remoteLaunchHint={remoteLaunchHint}
        />
        <RecapSection detail={detail} />
        <DigestSection detail={detail} />
        <ArtifactStats detail={detail} />
        <CommandsSection detail={detail} />
        <FilesChangedList detail={detail} onSelectFile={onSelectFile} />
        <CommitsAndTodos detail={detail} />
      </div>
    </div>
  );
}

function InspectorFooter({
  detail,
  remoteLaunchable,
  remoteResumeHint,
}: {
  detail: SessionDetail;
  remoteLaunchable: boolean;
  remoteResumeHint?: string;
}) {
  const [previewOpen, setPreviewOpen] = useState(false);
  const showTeamPreview =
    isLocalSession(detail) && teamPreviewEnabled(window.location.search) && !detail.repoLocalOnly;

  return (
    <SheetFooter className="bg-muted flex-row items-center justify-between gap-4 border-t">
      <div className="flex min-w-0 flex-col">
        <span className="text-xs">Resume this exact session</span>
        <span className="text-muted-foreground text-xs font-light">
          {isLocalSession(detail)
            ? `Reopens this session in ${getVendor(detail.agent).label} with its full context.`
            : remoteLaunchable
              ? `Opens this session in ${getVendor(detail.agent).label} through SSH.`
              : 'Available when the remote SSH host is connected.'}
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {showTeamPreview && (
          <>
            <Button variant="outline" onClick={() => setPreviewOpen(true)}>
              <EyeIcon /> Team sharing preview
            </Button>
            <SnapshotPreviewDialog
              detail={detail}
              open={previewOpen}
              onOpenChange={setPreviewOpen}
              previewOnly
            />
          </>
        )}
        <ResumeSessionButton detail={detail} disabledHint={remoteResumeHint} />
      </div>
    </SheetFooter>
  );
}

export function SessionInspector({
  session,
  sessionsVersion,
  synthesisSettingsKey,
  showMachineBadge = false,
  machines,
  onClose,
}: {
  session: Session | null;
  sessionsVersion: number;
  synthesisSettingsKey: string;
  showMachineBadge?: boolean;
  machines: MachineFact[];
  onClose: () => void;
}) {
  const { detail, loadError } = useSessionDetail(session, sessionsVersion, synthesisSettingsKey);
  const contentRef = useRef<HTMLDivElement>(null);
  const [selectedDiff, setSelectedDiff] = useState<FileSelection | null>(null);
  const {
    changes: fileChanges,
    isLoading: fileDiffLoading,
    loadError: fileDiffError,
  } = useFileDiff(selectedDiff);
  const isOpen = session != null;
  const openSessionKey = session == null ? null : sessionKey(session);
  const remoteLaunchable =
    detail != null &&
    !isLocalSession(detail) &&
    detail.cwd.trim() !== '' &&
    machines.some(
      (machine) =>
        machine.sourceId === detail.sourceId && (machine.state === 'ok' || machine.state === 'limited'),
    );
  const remoteMachine =
    detail == null ? undefined : machines.find((machine) => machine.sourceId === detail.sourceId);
  const remoteLaunchHint =
    detail == null || isLocalSession(detail) || remoteLaunchable
      ? undefined
      : remoteMachine?.state === 'connecting' || remoteMachine == null
        ? 'Checking SSH liveness…'
        : remoteMachine.state === 'ok' || remoteMachine.state === 'limited'
          ? 'Waiting for remote session details'
          : 'Remote is offline';
  const remoteResumeHint =
    detail == null ? undefined : resumeDisabledHint(detail, remoteLaunchable, remoteLaunchHint);

  useEffect(() => {
    setSelectedDiff(null);
  }, [openSessionKey]);

  return (
    <Sheet
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) {
          setSelectedDiff(null);
          onClose();
        }
      }}
    >
      <SheetContent
        ref={contentRef}
        tabIndex={-1}
        className="w-1/2! max-w-none! gap-0 outline-none"
        showCloseButton={true}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          contentRef.current?.focus();
        }}
      >
        {isOpen && detail != null && (
          <>
            <SheetHeader>
              <div className="flex min-w-0 flex-col gap-2">
                <div className="flex items-center gap-2 pr-10">
                  <SessionVendorBadge agent={detail.agent} />
                  <SheetTitle className="min-w-0 flex-1">
                    <SessionName name={detail.name} variant="inspector" />
                  </SheetTitle>
                  <SessionId id={detail.id} shortened />
                  {showMachineBadge && <MachineBadge label={detail.sourceLabel} />}
                </div>
                <HeaderMeta detail={detail} showMachineBadge={false} />
                <div className="border-b p-1" />
              </div>
            </SheetHeader>
            {loadError != null && (
              <div role="alert" className="text-destructive px-4 pb-2 text-xs">
                {loadError}
              </div>
            )}
            <InspectorBody
              detail={detail}
              remoteLaunchable={remoteLaunchable}
              remoteLaunchHint={remoteLaunchHint}
              onSelectFile={
                isLocalSession(detail)
                  ? (path) =>
                      setSelectedDiff({
                        sourceId: detail.sourceId,
                        agent: detail.agent,
                        sessionId: detail.id,
                        path,
                      })
                  : null
              }
            />
            <InspectorFooter
              detail={detail}
              remoteLaunchable={remoteLaunchable}
              remoteResumeHint={remoteResumeHint}
            />
          </>
        )}
      </SheetContent>
      <Sheet
        open={filePanelOpen(selectedDiff, detail)}
        onOpenChange={(open) => {
          if (!open) setSelectedDiff(null);
        }}
      >
        <SheetContent className="w-1/2! max-w-none! gap-0" showCloseButton={true}>
          {selectedDiff != null && (
            <>
              <SheetHeader className="border-b">
                <div className="flex min-w-0 items-center gap-2 pr-10">
                  <span className="text-brand shrink-0 text-xs font-semibold tracking-wide">
                    FILE CHANGES
                  </span>
                  <SheetTitle className="min-w-0 flex-1 truncate font-mono text-xs" title={selectedDiff.path}>
                    {selectedDiff.path}
                  </SheetTitle>
                </div>
              </SheetHeader>
              <DiffList changes={fileChanges} isLoading={fileDiffLoading} loadError={fileDiffError} />
            </>
          )}
        </SheetContent>
      </Sheet>
    </Sheet>
  );
}
