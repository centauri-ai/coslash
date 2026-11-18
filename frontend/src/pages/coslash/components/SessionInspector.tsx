import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import {
  ChevronDownIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  LoaderCircleIcon,
  PlayIcon,
  SquareCheckIcon,
  SquareIcon,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Dialog, DialogTrigger } from '@/components/ui/dialog';
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { DiffList } from '@/pages/coslash/components/DiffList';
import { DirectedHandoffDialog } from '@/pages/coslash/components/DirectedHandoffDialog';
import { DirectedHandoffStatus } from '@/pages/coslash/components/DirectedHandoffStatus';
import {
  DetailLoadError,
  SnapshotRefreshStatus,
  SnapshotStalenessNotice,
  SummaryOnlyBanner,
} from '@/pages/coslash/components/session-inspector/DetailStatus';
import {
  HeaderMeta,
  SessionInspectorTitle,
} from '@/pages/coslash/components/session-inspector/InspectorHeader';
import {
  branchDriftReadiness,
  contextFillReadiness,
  treeStaleReadiness,
} from '@/pages/coslash/components/session-inspector/readiness';
import { PromptCacheCell, ReadinessCell } from '@/pages/coslash/components/session-inspector/ReadinessCells';
import { FieldLabel, SectionLabel } from '@/pages/coslash/components/session-inspector/SectionLabel';
import { SubagentDialogContent, SubagentModelBadge } from '@/pages/coslash/components/SessionCard';
import { useLaunchTerminal } from '@/pages/coslash/hooks/use-launch-terminal';
import { useSessionDetail } from '@/pages/coslash/hooks/use-session-detail';
import { useFileDiff, type FileSelection } from '@/pages/coslash/hooks/use-sessions';
import {
  blocksFromTexts,
  collapseDebriefBlocks,
  parseDebriefText,
  type DebriefBlock,
} from '@/pages/coslash/lib/debrief-text';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';
import {
  digestDateKey,
  formatDigestDateDivider,
  formatDigestDateRange,
  formatDigestTime,
} from '@/pages/coslash/lib/format';
import { copyHandoffText, handoffBrief } from '@/pages/coslash/lib/handoff';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  canResumeSession,
  getSessionOutcome,
  getVendor,
  goalSourceLabel,
  isLocalSession,
  remoteLaunchDisabledHint,
  resolveGoal,
  resumeDisabled,
  resumeDisabledHint,
  sessionDisplayTitle,
  sessionKey,
  sessionReadiness,
  SUBAGENT_STATUSES,
  subagentParentName,
  type DigestEntry,
  type Session,
  type SessionDetail,
} from '@/pages/coslash/lib/session';
import {
  cachedOfflineWarning,
  filePanelOpen,
  refreshSourceAndRetry,
  snapshotMayBeStale,
} from '@/pages/coslash/lib/session-detail';
import { SkillsSection } from './SkillsSection';

/* Brand-filled CTA. Replaces the Button default variant's fill, text, and hover as
   one set: overriding only the fill leaves the variant's hover:bg-primary in place,
   which drops the button to the shadcn neutral on hover. Hover mixes toward
   --foreground so it gains contrast against the surface in either theme. */
const brandCta =
  'bg-brand text-brand-foreground hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]';

function LaunchError({ message }: { message: string | null }) {
  if (message == null) return null;
  return <span className="text-danger-fg text-xs">{message}</span>;
}

function DisabledLaunchTooltip({ hint, children }: { hint?: string; children: ReactNode }) {
  if (hint == null) return children;
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span tabIndex={0}>{children}</span>
        </TooltipTrigger>
        <TooltipContent className="coslash-shell">{hint}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function GrokFreshSessionControl({
  launching,
  launchError,
  onLaunch,
}: {
  launching: boolean;
  launchError: string | null;
  onLaunch: () => void;
}) {
  return (
    <div className="flex w-fit max-w-full flex-wrap items-center gap-2">
      <Button
        className="bg-brand text-brand-foreground w-fit p-2 text-xs hover:bg-[color-mix(in_oklch,var(--brand),var(--foreground)_10%)]"
        disabled={launching}
        aria-busy={launching}
        onClick={onLaunch}
      >
        <PlayIcon />
        <span>Start fresh</span>
      </Button>
      {launchError && (
        <span role="alert" className="text-danger-fg text-xs">
          {launchError}
        </span>
      )}
    </div>
  );
}

function GrokFreshSessionButton({ detail }: { detail: SessionDetail }) {
  const { launch, launchError, launching } = useLaunchTerminal(detail);
  return (
    <GrokFreshSessionControl launching={launching} launchError={launchError} onLaunch={() => launch('new')} />
  );
}

function ResumeSessionButton({ detail, disabledHint }: { detail: SessionDetail; disabledHint?: string }) {
  const { launch, launchError } = useLaunchTerminal(detail);
  const disabled = resumeDisabled(detail, disabledHint);
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';
  const recommended = canResumeSession(detail) && sessionReadiness(detail).key === 'resume';

  return (
    <div className="flex flex-col gap-1">
      <DisabledLaunchTooltip hint={disabledHint}>
        <Button
          variant={recommended ? 'default' : 'outline'}
          className={cn('w-fit p-2 text-xs', { [brandCta]: recommended })}
          onClick={() => launch(opensCursor ? 'open' : 'resume')}
          disabled={disabled}
        >
          {opensCursor ? <ExternalLinkIcon /> : <PlayIcon />}
          <span>{opensCursor ? 'Open Cursor' : 'Resume'}</span>
        </Button>
      </DisabledLaunchTooltip>
      <LaunchError message={launchError} />
    </div>
  );
}

function HandoffSection({
  detail,
  exactDetailsAvailable,
  remoteLaunchable,
  remoteLaunchHint,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState<string | null>(null);
  const contextFill = contextFillReadiness(detail);
  const branchDrift = branchDriftReadiness(detail.git);
  const treeStale = treeStaleReadiness(detail.lastEditAt);

  const brief = handoffBrief(detail);

  const copyBrief = async (text = brief) => {
    setCopyError(null);
    try {
      await copyHandoffText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
      return true;
    } catch {
      setCopied(false);
      setCopyError('Could not copy the handoff. Allow clipboard access and try again.');
      return false;
    }
  };

  return (
    <div className="@container flex flex-col gap-2">
      <SectionLabel title={detail.agent === 'grok' ? 'RESUME OR START FRESH' : 'RESUME OR HAND OFF'} />
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border @[560px]:grid-cols-5">
        <ReadinessCell label="Context used" value={contextFill?.value} tone={contextFill?.tone} />
        <ReadinessCell label="Compactions" value={String(detail.compactions)} />
        <ReadinessCell label="Branch" value={branchDrift?.value} tone={branchDrift?.tone} />
        <ReadinessCell label="Working tree" value={treeStale?.value} tone={treeStale?.tone} />
        <PromptCacheCell lastAccessAt={detail.mtime} className="col-span-2 @[560px]:col-span-1" />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {detail.agent === 'grok' ? (
          <GrokFreshSessionButton detail={detail} />
        ) : (
          <DirectedHandoffDialog
            source={detail}
            disabledHint={
              !isLocalSession(detail) && detail.agent === 'pi'
                ? 'Pi launch is available locally only'
                : !isLocalSession(detail) && !remoteLaunchable
                  ? remoteLaunchHint
                  : undefined
            }
            onStarted={onHandoffStarted}
          />
        )}
        <Button variant="outline" className="w-fit p-2 text-xs" onClick={() => void copyBrief()}>
          <span>Copy handoff</span>
        </Button>
        {copied && <span className="text-coslash-muted text-xs">copied to clipboard</span>}
        {copyError && (
          <span role="alert" className="text-danger-fg text-xs">
            {copyError}
          </span>
        )}
      </div>
      {handoff && (
        <div className="rounded-lg border p-3">
          <DirectedHandoffStatus handoff={handoff} onOpenTarget={onOpenTarget} />
        </div>
      )}
      {!isLocalSession(detail) && (
        <div className="text-coslash-muted text-xs">
          {!exactDetailsAvailable
            ? 'This inspector uses the bounded session-library summary. Complete commands and exact file diffs are unavailable for this session.'
            : detail.agent === 'pi'
              ? 'Pi terminal actions are available locally only. You can still copy this handoff.'
              : remoteLaunchable
                ? 'Remote terminal actions open through SSH. Exact cached details, commands, and file diffs stay available locally; synthesis and Hub sharing remain local-only.'
                : 'Remote terminal actions are available when SSH reconnects. Exact cached details, commands, and file diffs stay available locally; synthesis and Hub sharing remain local-only.'}
        </div>
      )}
    </div>
  );
}

function RecapSection({ detail }: { detail: SessionDetail }) {
  const goal = resolveGoal(detail);
  const synthesizing = detail.synthesis == null && detail.synthesisPending;
  const synthesisPlaceholder = (
    <div className="text-coslash-muted flex items-center gap-1 pt-1 text-xs">
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
          <span className="text-coslash-muted text-xs">GOAL</span>
          <Badge variant="secondary" className="text-xs">
            {goalSourceLabel(goal.source)}
          </Badge>
        </div>
        <div className="pt-2">
          <DebriefProse key={`${sessionKey(detail)}:goal`} blocks={blocksFromTexts(goal.texts)} tone="goal" />
        </div>
        <div className="border-b pt-3" />
        <div className="text-coslash-muted pt-3 text-xs">OUTCOME</div>
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
        <div className="text-coslash-muted pt-3 text-xs">KEY DECISIONS</div>
        {synthesizing ? (
          synthesisPlaceholder
        ) : detail.synthesis?.keyDecisions.length ? (
          <div className="flex flex-col gap-1 pt-1">
            {detail.synthesis.keyDecisions.map((decision) => (
              <div key={decision} className="flex items-start gap-2 text-xs">
                <span className="bg-coslash-muted mt-1 size-1 shrink-0 rounded-full" />
                <span>{decision}</span>
              </div>
            ))}
          </div>
        ) : (
          <div className="pt-1 text-xs">—</div>
        )}
        {detail.synthesis?.nextStep && (
          <>
            <div className="text-coslash-muted pt-3 text-xs">NEXT STEP</div>
            <div className="pt-1 text-xs">{detail.synthesis.nextStep}</div>
          </>
        )}
      </div>
    </div>
  );
}

const DEBRIEF_PREVIEW_UNITS = 3;
/** Must stay in sync with the docked-inspector breakpoint in coslash-layout.css. */
const DOCKED_INSPECTOR_QUERY = '(min-width: 1720px)';
const INSPECTOR_WIDTH_KEY = 'coslash.inspector-width.v1';
const MIN_INSPECTOR_WIDTH = 360;
const MAX_INSPECTOR_VIEWPORT_SHARE = 0.8;
const DEFAULT_INSPECTOR_WIDTH = 'clamp(420px, 32vw, 760px)';
const INSPECTOR_KEYBOARD_STEP = 16;

function maximumInspectorWidth(viewportWidth: number): number {
  return Math.max(MIN_INSPECTOR_WIDTH, Math.round(viewportWidth * MAX_INSPECTOR_VIEWPORT_SHARE));
}

function clampInspectorWidth(width: number, viewportWidth = window.innerWidth): number {
  return Math.round(Math.min(Math.max(width, MIN_INSPECTOR_WIDTH), maximumInspectorWidth(viewportWidth)));
}

function defaultInspectorWidth(viewportWidth: number): number {
  return clampInspectorWidth(Math.min(Math.max(viewportWidth * 0.32, 420), 760), viewportWidth);
}

// oxlint-disable-next-line react/only-export-components -- exported for focused keyboard tests
export function inspectorWidthForKey(
  key: string,
  currentWidth: number,
  viewportWidth: number,
): number | null {
  if (key === 'Home') return MIN_INSPECTOR_WIDTH;
  if (key === 'End') return maximumInspectorWidth(viewportWidth);
  if (key === 'ArrowLeft') {
    return clampInspectorWidth(currentWidth + INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  if (key === 'ArrowRight') {
    return clampInspectorWidth(currentWidth - INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  return null;
}

function readInspectorWidth(): number | null {
  try {
    const stored = Number(window.localStorage.getItem(INSPECTOR_WIDTH_KEY));
    return Number.isFinite(stored) && stored > 0 ? Math.max(stored, MIN_INSPECTOR_WIDTH) : null;
  } catch {
    return null;
  }
}

export function DebriefProse({
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
        className={cn('border-coslash-line border-l-2 pl-3', {
          'border-l-brand': tone === 'goal',
          'border-l-recap': tone === 'outcome',
        })}
      >
        <DebriefBlocks blocks={shown} tone={tone} />
      </div>
      {canExpand && (
        <button
          type="button"
          aria-expanded={expanded}
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
        </button>
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
                  <span className="bg-coslash-muted mt-1.5 size-1 shrink-0 rounded-full" />
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

const DIGEST_CATEGORIES: Record<DigestCategory, { label: string; fg: string; dot: string }> = {
  first_prompt: { label: 'FIRST PROMPT', fg: 'text-success-fg', dot: 'bg-success-fg' },
  question: { label: 'QUESTION', fg: 'text-question', dot: 'bg-question' },
  subagent: { label: 'Subagent', fg: 'text-subagent', dot: 'bg-subagent' },
  todos: { label: 'TODOS', fg: 'text-coslash-muted', dot: 'bg-coslash-muted' },
  recap: { label: 'RECAP', fg: 'text-recap', dot: 'bg-recap' },
  plan: { label: 'PLAN', fg: 'text-recap', dot: 'bg-recap' },
  user: { label: 'USER TURN', fg: 'text-brand', dot: 'bg-brand' },
  compaction: { label: 'COMPACTION', fg: 'text-compaction', dot: 'bg-compaction' },
};

function CategoryChip({
  category,
  count,
  active,
  onCycle,
}: {
  category: DigestCategory;
  count: number;
  active: boolean;
  onCycle: () => void;
}) {
  const meta = DIGEST_CATEGORIES[category];
  return (
    <button
      type="button"
      aria-pressed={active}
      title={`Click to show only ${meta.label.toLowerCase()}, click again to exclude, and once more to show all.`}
      className={cn('flex cursor-pointer items-center gap-1 rounded-full border px-2 py-1 select-none', {
        'bg-coslash-soft': active,
        'border-dashed': !active,
      })}
      onClick={onCycle}
    >
      <span className={cn('size-2 rounded-full', active ? meta.dot : 'bg-coslash-neutral-dot')} />
      <span
        className={cn('text-xs font-semibold', {
          'text-coslash-muted': !active,
        })}
      >
        {meta.label}
      </span>
      <span className="text-coslash-muted text-xs">{count}</span>
    </button>
  );
}

function DigestRow({ entry, endsDay }: { entry: DigestEntry; endsDay?: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const meta = DIGEST_CATEGORIES[entry.category];
  const collapsible = entry.description.length > 120;

  return (
    <div className={cn('border-coslash-line flex items-baseline gap-2 py-1', { 'border-b': !endsDay })}>
      <span className={cn('w-24 shrink-0 text-xs font-bold tracking-wide', meta.fg)}>{meta.label}</span>
      <div className="min-w-0 flex-1">
        <div
          className={cn('text-xs wrap-break-word', {
            'line-clamp-1': collapsible && !expanded,
            'whitespace-pre-wrap': entry.category === 'plan',
          })}
        >
          {entry.branchId && (
            <span
              className="text-coslash-muted inline-block max-w-40 truncate align-bottom"
              title={entry.branchId}
            >
              [branch {entry.branchId}]{' '}
            </span>
          )}
          {entry.inherited && <span className="text-coslash-muted">[inherited] </span>}
          {entry.active === true && <span className="text-coslash-muted">[active] </span>}
          {entry.description}
        </div>
        {entry.answer != null && (
          <div className="border-coslash-line text-coslash-muted border-l-2 pl-2 text-xs wrap-break-word">
            {entry.answer}
          </div>
        )}
        {collapsible && (
          <button
            type="button"
            aria-expanded={expanded}
            className="text-brand flex cursor-pointer items-center gap-1 pt-1 text-xs"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
            <span>{expanded ? 'collapse' : `expand full ${entry.category}`}</span>
          </button>
        )}
      </div>
      <span className="text-coslash-muted flex shrink-0 flex-col items-end font-mono text-xs whitespace-nowrap">
        <span>turn {entry.turn}</span>
        {entry.time != null && entry.time > 0 && (
          <span className="text-coslash-muted">{formatDigestTime(entry.time)}</span>
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
  const parentName = subagentParentName(subagent, detail.subagents, detail.name);
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
            <div className="text-coslash-muted truncate pt-1 text-xs">
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
    <div className="text-coslash-muted grid grid-cols-[1fr_auto_1fr] items-center gap-2 pt-2 text-xs font-bold tracking-wide">
      <span className="bg-coslash-line h-px" />
      <span>{label}</span>
      <span className="bg-coslash-line h-px" />
    </div>
  );
}

export function DigestSection({ detail }: { detail: SessionDetail }) {
  const [categoryFilter, setCategoryFilter] = useState<{
    category: DigestCategory;
    mode: 'only' | 'exclude';
  } | null>(null);
  const digest = detail.digest;
  if (digest.length === 0)
    return detail.detailsIncomplete ? (
      <p className="text-warning-fg pb-2 text-xs">Some nested tool details are unavailable.</p>
    ) : null;

  const counts = new Map<DigestCategory, number>();
  for (const entry of digest) counts.set(entry.category, (counts.get(entry.category) ?? 0) + 1);

  const cycleCategory = (category: DigestCategory) => {
    setCategoryFilter((current) => {
      if (current?.category !== category) return { category, mode: 'only' };
      return current.mode === 'only' ? { category, mode: 'exclude' } : null;
    });
  };
  const categoryVisible = (category: DigestCategory) => {
    if (categoryFilter == null) return true;
    return categoryFilter.mode === 'only'
      ? category === categoryFilter.category
      : category !== categoryFilter.category;
  };
  const visible = digest.filter((entry) => categoryVisible(entry.category));
  const times = digest.map((e) => e.time ?? 0).filter((t) => t > 0);
  const dateRange = formatDigestDateRange(times);

  const startsDayIndices = new Set<number>();
  const endsDayIndices = new Set<number>();
  let lastDate = '';
  for (let i = 0; i < visible.length; i++) {
    const entryDate = visible[i].time != null && visible[i].time! > 0 ? digestDateKey(visible[i].time!) : '';
    if (entryDate !== '' && entryDate !== lastDate) {
      startsDayIndices.add(i);
    }
    if (i > 0 && entryDate !== '' && lastDate !== '' && entryDate !== lastDate) {
      endsDayIndices.add(i - 1);
    }
    if (entryDate !== '') {
      lastDate = entryDate;
    }
  }

  const rows = visible.map((entry, index) => {
    return (
      <div key={entry.sourceEntryId ? `${entry.sourceEntryId}:${entry.category}` : index}>
        {startsDayIndices.has(index) && <DateDivider label={formatDigestDateDivider(entry.time!)} />}
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
          <span className="text-coslash-muted text-xs font-semibold tracking-wide">TIMELINE</span>
          <span className="text-coslash-muted text-xs">
            {detail.agent === 'pi' ? 'all branches in time order' : 'key events from this session'}
          </span>
        </div>
        <span className="text-coslash-muted text-xs">{dateRange}</span>
      </div>
      <div className="flex flex-wrap gap-1 pb-2">
        {(Object.keys(DIGEST_CATEGORIES) as DigestCategory[])
          .filter((category) => counts.has(category))
          .map((category) => (
            <CategoryChip
              key={category}
              category={category}
              count={counts.get(category)!}
              active={categoryVisible(category)}
              onCycle={() => cycleCategory(category)}
            />
          ))}
      </div>
      {detail.detailsIncomplete && (
        <p className="text-warning-fg pb-2 text-xs">Some nested tool details are unavailable.</p>
      )}
      <div className="flex flex-col gap-1">{rows}</div>
    </div>
  );
}

function StatCell({ value, label }: { value: string; label: string }) {
  return (
    <div className="bg-coslash-surface p-2">
      <div className="text-base font-bold">{value}</div>
      <div className="text-coslash-muted text-xs">{label}</div>
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
      <div className="bg-coslash-line grid grid-cols-3 gap-px overflow-hidden rounded-lg border">
        {cells.map(([value, label]) => (
          <StatCell key={label} value={value} label={label} />
        ))}
      </div>
    </div>
  );
}

function FilesChangedList({
  detail,
  onSelectFile,
}: {
  detail: SessionDetail;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
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
                className={cn('text-coslash-muted min-w-0 truncate text-left', {
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
                  'text-coslash-muted min-w-0 cursor-pointer truncate text-left hover:underline',
                  {
                    'text-success-fg': fileEdit.isNew,
                  },
                )}
                onClick={(event) => onSelectFile(fileEdit, event.currentTarget)}
              >
                {fileEdit.path.split('/').pop()}
              </button>
            )}
            <span className="whitespace-nowrap">
              <span className="text-success-fg">+{fileEdit.adds}</span>{' '}
              <span className="text-danger-fg">−{fileEdit.dels}</span>{' '}
              <span className="text-coslash-muted">
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
          <div className="text-coslash-muted text-xs">—</div>
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
          <div className="text-coslash-muted text-xs">none</div>
        ) : (
          detail.todos.map((todo, index) => (
            <div
              key={index}
              className={cn('flex items-start gap-1 py-1 text-xs', {
                'text-coslash-muted': todo.done,
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
      <button
        type="button"
        aria-expanded={open}
        className="flex w-full cursor-pointer items-center gap-2"
        onClick={() => setOpen(!open)}
      >
        {open ? (
          <ChevronDownIcon className="text-coslash-muted size-3" />
        ) : (
          <ChevronRightIcon className="text-coslash-muted size-3" />
        )}
        <span className="text-coslash-muted pb-1 text-xs font-semibold tracking-wide">
          {`${detail.commands.length} COMMANDS`}
        </span>
        <span className="flex-1 border-b" />
      </button>
      {open && (
        <div className="bg-coslash-soft max-h-44 overflow-x-hidden overflow-y-auto rounded-lg p-3">
          <pre className="text-coslash-ink font-mono text-xs leading-relaxed wrap-break-word whitespace-pre-wrap">
            {detail.commands.join('\n')}
          </pre>
        </div>
      )}
    </div>
  );
}

function InspectorBody({
  detail,
  exactDetailsAvailable,
  onSelectFile,
  remoteLaunchable,
  remoteLaunchHint,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  detail: SessionDetail;
  exactDetailsAvailable: boolean;
  onSelectFile: ((fileEdit: SessionDetail['fileEdits'][number], trigger: HTMLButtonElement) => void) | null;
  remoteLaunchable: boolean;
  remoteLaunchHint?: string;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const currentSessionKey = sessionKey(detail);
  const [tabSelection, setTabSelection] = useState<{
    sessionKey: string;
    tab: 'session' | 'files' | 'skills';
  }>(() => ({ sessionKey: currentSessionKey, tab: 'session' }));
  if (tabSelection.sessionKey !== currentSessionKey) {
    setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  }
  const selectedTab = tabSelection.sessionKey === currentSessionKey ? tabSelection.tab : 'session';
  const selectedTabAvailable =
    selectedTab === 'session' ||
    (selectedTab === 'files' && detail.fileEdits.length > 0) ||
    (selectedTab === 'skills' && (detail.skills?.length ?? 0) > 0);
  if (!selectedTabAvailable) setTabSelection({ sessionKey: currentSessionKey, tab: 'session' });
  const activeTab = selectedTabAvailable ? selectedTab : 'session';
  const tabClassName =
    'text-coslash-muted h-auto flex-none rounded-none px-0 py-3 data-[state=active]:text-coslash-ink data-[state=active]:after:opacity-100 group-data-horizontal/tabs:after:bottom-0';
  // scroll on the outer div, layout on the inner one — flex children of a
  // scroll container shrink to fit instead of overflowing, which collapses
  // the overflow-hidden stat grids
  return (
    <Tabs
      key={currentSessionKey}
      value={activeTab}
      onValueChange={(value) =>
        setTabSelection({ sessionKey: currentSessionKey, tab: value as 'session' | 'files' | 'skills' })
      }
      className="min-h-0 flex-1 gap-0"
    >
      <TabsList
        variant="line"
        aria-label="Session views"
        className="w-full shrink-0 justify-start gap-4 border-b px-4 py-0 group-data-horizontal/tabs:h-auto"
      >
        <TabsTrigger value="session" className={tabClassName}>
          Session
        </TabsTrigger>
        {detail.fileEdits.length > 0 && (
          <TabsTrigger value="files" className={tabClassName}>
            Files · {detail.fileEdits.length}
          </TabsTrigger>
        )}
        {(detail.skills?.length ?? 0) > 0 && (
          <TabsTrigger value="skills" className={tabClassName}>
            Skills
          </TabsTrigger>
        )}
      </TabsList>
      <div className="flex-1 overflow-x-hidden overflow-y-auto">
        <TabsContent
          value="session"
          forceMount
          className="flex flex-col gap-2 px-4 py-4 data-[state=inactive]:hidden"
        >
          <HandoffSection
            detail={detail}
            exactDetailsAvailable={exactDetailsAvailable}
            remoteLaunchable={remoteLaunchable}
            remoteLaunchHint={remoteLaunchHint}
            handoff={handoff}
            onHandoffStarted={onHandoffStarted}
            onOpenTarget={onOpenTarget}
          />
          <RecapSection detail={detail} />
          <ArtifactStats detail={detail} />
          <CommandsSection detail={detail} />
          <CommitsAndTodos detail={detail} />
          <DigestSection detail={detail} />
        </TabsContent>
        <TabsContent value="files" forceMount className="px-4 py-4 data-[state=inactive]:hidden">
          <FilesChangedList detail={detail} onSelectFile={onSelectFile} />
        </TabsContent>
        <TabsContent value="skills" forceMount className="px-4 pb-4 data-[state=inactive]:hidden">
          <SkillsSection skills={detail.skills} />
        </TabsContent>
      </div>
    </Tabs>
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
  const opensCursor =
    isLocalSession(detail) && detail.agent === 'cursor' && detail.entrypoint === 'cursor-ide';

  return (
    <SheetFooter className="bg-coslash-soft flex-row items-center justify-between gap-4 border-t">
      <div className="flex min-w-0 flex-col">
        <span className="text-xs">
          {opensCursor ? 'Open workspace in Cursor' : 'Resume this exact session'}
        </span>
        <span className="text-coslash-muted text-xs font-light">
          {isLocalSession(detail)
            ? opensCursor
              ? 'Cursor does not expose an IDE deep link, so this opens the workspace without restoring this chat.'
              : `Reopens this session in ${getVendor(detail.agent).label} with its full context.`
            : detail.agent === 'pi'
              ? 'Pi CLI resume is available locally only.'
              : remoteLaunchable
                ? `Opens this session in ${getVendor(detail.agent).label} through SSH.`
                : 'Available when the remote SSH host is connected.'}
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <ResumeSessionButton detail={detail} disabledHint={remoteResumeHint} />
      </div>
    </SheetFooter>
  );
}

export function SessionInspector({
  session,
  sessionsVersion,
  synthesisSettingsKey,
  synthesisCostVersion,
  showMachineBadge = false,
  machines,
  onRefresh,
  onClose,
  handoff,
  onHandoffStarted,
  onOpenTarget,
}: {
  session: Session | null;
  sessionsVersion: number;
  synthesisSettingsKey: string;
  synthesisCostVersion: string | null;
  showMachineBadge?: boolean;
  machines: MachineFact[];
  onRefresh: () => void | Promise<void>;
  onClose: () => void;
  handoff?: DirectedHandoff;
  onHandoffStarted: () => void;
  onOpenTarget: (handoff: DirectedHandoff) => void;
}) {
  const [detailRetryToken, setDetailRetryToken] = useState(0);
  const detailAttemptIdentity = session == null ? null : `${sessionKey(session)}@${detailRetryToken}`;
  const detailAttemptRef = useRef(detailAttemptIdentity);
  useLayoutEffect(() => {
    detailAttemptRef.current = detailAttemptIdentity;
  }, [detailAttemptIdentity]);
  const { detail, isLoading, loadError, loadErrorKind, cachedOffline, summaryOnly, synthesisSettledCount } =
    useSessionDetail(session, detailRetryToken, sessionsVersion, synthesisSettingsKey);
  const contentRef = useRef<HTMLDivElement>(null);
  const resizeRef = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
    currentWidth: number;
  } | null>(null);
  const [selectedDiffState, setSelectedDiff] = useState<FileSelection | null>(null);
  const diffTriggerRef = useRef<HTMLButtonElement | null>(null);
  const selectedDiff = filePanelOpen(selectedDiffState, detail) ? selectedDiffState : null;
  const [fileDiffRetryToken, setFileDiffRetryToken] = useState(0);
  const [modal, setModal] = useState(() =>
    typeof window !== 'undefined' && typeof window.matchMedia === 'function'
      ? !window.matchMedia(DOCKED_INSPECTOR_QUERY).matches
      : true,
  );
  const [width, setWidth] = useState<number | null>(readInspectorWidth);
  const [viewportWidth, setViewportWidth] = useState(() =>
    typeof window === 'undefined' ? 1375 : window.innerWidth,
  );

  const persistWidth = (nextWidth: number) => {
    setWidth(nextWidth);
    try {
      window.localStorage.setItem(INSPECTOR_WIDTH_KEY, String(nextWidth));
    } catch {
      // a blocked store just means the width resets next session
    }
  };

  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    const startWidth = clampInspectorWidth(contentRef.current?.offsetWidth ?? MIN_INSPECTOR_WIDTH);
    resizeRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth,
      currentWidth: startWidth,
    };
  };
  const continueResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resize.currentWidth = clampInspectorWidth(resize.startWidth + resize.startX - event.clientX);
    setWidth(resize.currentWidth);
  };
  const finishResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resizeRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    persistWidth(resize.currentWidth);
  };
  const resizeWithKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const currentWidth = contentRef.current?.offsetWidth ?? width ?? MIN_INSPECTOR_WIDTH;
    const nextWidth = inspectorWidthForKey(event.key, currentWidth, viewportWidth);
    if (nextWidth == null) return;
    event.preventDefault();
    persistWidth(nextWidth);
  };
  const {
    changes: fileChanges,
    isLoading: fileDiffLoading,
    loadError: fileDiffError,
    loadErrorKind: fileDiffErrorKind,
  } = useFileDiff(selectedDiff, fileDiffRetryToken);
  const isOpen = session != null;
  const remoteLaunchable =
    detail != null &&
    !isLocalSession(detail) &&
    detail.cwd.trim() !== '' &&
    detail.launchable &&
    machines.some(
      (machine) =>
        machine.sourceId === detail.sourceId && (machine.state === 'ok' || machine.state === 'limited'),
    );
  const remoteMachine =
    detail == null ? undefined : machines.find((machine) => machine.sourceId === detail.sourceId);
  const showCachedOffline = cachedOfflineWarning(cachedOffline, remoteMachine?.state);
  const remoteLaunchHint =
    detail == null || isLocalSession(detail) || remoteLaunchable
      ? undefined
      : remoteLaunchDisabledHint(remoteMachine?.state, detail.launchBlockReason);
  const remoteResumeHint =
    detail == null ? undefined : resumeDisabledHint(detail, remoteLaunchable, remoteLaunchHint);

  const refreshDetailSource = () => {
    const startingIdentity = detailAttemptIdentity;
    void refreshSourceAndRetry(
      onRefresh,
      () => setDetailRetryToken((token) => token + 1),
      () => startingIdentity != null && detailAttemptRef.current === startingIdentity,
    );
  };

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const media = window.matchMedia(DOCKED_INSPECTOR_QUERY);
    const updateModal = () => setModal(!media.matches);
    const updateViewportWidth = () => setViewportWidth(window.innerWidth);
    media.addEventListener('change', updateModal);
    window.addEventListener('resize', updateViewportWidth);
    return () => {
      media.removeEventListener('change', updateModal);
      window.removeEventListener('resize', updateViewportWidth);
    };
  }, []);

  const inspectorWidth =
    width == null ? DEFAULT_INSPECTOR_WIDTH : `min(${width}px, ${MAX_INSPECTOR_VIEWPORT_SHARE * 100}vw)`;
  const ariaWidth = clampInspectorWidth(width ?? defaultInspectorWidth(viewportWidth), viewportWidth);
  const ariaMaximumWidth = maximumInspectorWidth(viewportWidth);

  useEffect(() => {
    if (!isOpen) return;
    const root = document.documentElement;
    root.style.setProperty('--coslash-inspector-width', inspectorWidth);
    return () => {
      root.style.removeProperty('--coslash-inspector-width');
    };
  }, [isOpen, inspectorWidth]);
  return (
    <Sheet
      modal={modal}
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) {
          setSelectedDiff(null);
          setDetailRetryToken((token) => token + 1);
          onClose();
        }
      }}
    >
      <SheetContent
        ref={contentRef}
        tabIndex={-1}
        className="coslash-shell w-full! max-w-none! gap-0 outline-none sm:w-[var(--coslash-inspector-w)]!"
        style={{ '--coslash-inspector-w': inspectorWidth } as CSSProperties}
        showCloseButton={true}
        onInteractOutside={(event) => {
          if (!modal) event.preventDefault();
        }}
        onOpenAutoFocus={(event) => {
          event.preventDefault();
          contentRef.current?.focus();
        }}
      >
        <div
          role="separator"
          tabIndex={0}
          aria-orientation="vertical"
          aria-label="Resize inspector"
          aria-valuemin={MIN_INSPECTOR_WIDTH}
          aria-valuemax={ariaMaximumWidth}
          aria-valuenow={ariaWidth}
          onPointerDown={startResize}
          onPointerMove={continueResize}
          onPointerUp={finishResize}
          onPointerCancel={finishResize}
          onLostPointerCapture={finishResize}
          onKeyDown={resizeWithKeyboard}
          className="hover:bg-coslash-accent focus-visible:bg-coslash-accent absolute inset-y-0 left-0 z-20 hidden w-1.5 cursor-col-resize touch-none transition-colors outline-none sm:block"
        />
        {session != null && <SheetTitle className="sr-only">{sessionDisplayTitle(session)}</SheetTitle>}
        {isOpen && detail == null && isLoading && (
          <div
            role="status"
            className="text-coslash-muted flex flex-1 items-center justify-center gap-2 text-xs"
          >
            <LoaderCircleIcon className="size-4 animate-spin" />
            Loading exact session details…
          </div>
        )}
        {isOpen && detail == null && loadError != null && loadErrorKind != null && (
          <DetailLoadError
            message={loadError}
            kind={loadErrorKind}
            onRetry={() => setDetailRetryToken((token) => token + 1)}
            onRefresh={refreshDetailSource}
          />
        )}
        {isOpen && session != null && detail != null && (
          <>
            <SheetHeader>
              <div className="flex min-w-0 flex-col gap-2">
                <SessionInspectorTitle detail={detail} showMachineBadge={showMachineBadge} />
                <HeaderMeta
                  detail={detail}
                  showMachineBadge={false}
                  synthesisCostVersion={synthesisCostVersion}
                  synthesisSettledCount={synthesisSettledCount}
                />
                {session != null && snapshotMayBeStale(detail, session) && <SnapshotStalenessNotice />}
              </div>
            </SheetHeader>
            <SnapshotRefreshStatus
              isLoading={isLoading}
              error={loadError}
              kind={loadErrorKind}
              onRetry={
                loadErrorKind === 'authentication'
                  ? undefined
                  : () => setDetailRetryToken((token) => token + 1)
              }
              onRefresh={refreshDetailSource}
            />
            {showCachedOffline && (
              <div
                role="status"
                className="text-warning-fg bg-warning-bg mx-4 mb-2 rounded-sm px-3 py-2 text-xs"
              >
                Showing the last complete cached details; the SSH workspace is not fully synced.
              </div>
            )}
            {summaryOnly && <SummaryOnlyBanner />}
            <InspectorBody
              detail={detail}
              exactDetailsAvailable={!summaryOnly}
              remoteLaunchable={remoteLaunchable}
              remoteLaunchHint={remoteLaunchHint}
              handoff={handoff}
              onHandoffStarted={onHandoffStarted}
              onOpenTarget={onOpenTarget}
              onSelectFile={
                summaryOnly
                  ? null
                  : (fileEdit, trigger) => {
                      diffTriggerRef.current = trigger;
                      setSelectedDiff({
                        sourceId: detail.sourceId,
                        agent: detail.agent,
                        sessionId: detail.id,
                        revision: detail.detailRevision,
                        path: fileEdit.path,
                        changeIds: fileEdit.changeIds ?? [],
                      });
                    }
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
        <SheetContent
          className="coslash-shell w-full! max-w-none! gap-0 sm:w-1/2!"
          showCloseButton={true}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            const trigger = diffTriggerRef.current;
            (trigger?.isConnected ? trigger : contentRef.current)?.focus();
          }}
        >
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
              <DiffList
                changes={fileChanges}
                isLoading={fileDiffLoading}
                loadError={fileDiffError}
                showRefresh={
                  fileDiffErrorKind === 'stale' ||
                  fileDiffErrorKind === 'missing' ||
                  fileDiffErrorKind === 'corrupt'
                }
                showRetry={fileDiffErrorKind === 'other'}
                onRetry={() => setFileDiffRetryToken((token) => token + 1)}
                onRefresh={() => {
                  setSelectedDiff(null);
                  refreshDetailSource();
                }}
              />
            </>
          )}
        </SheetContent>
      </Sheet>
    </Sheet>
  );
}
