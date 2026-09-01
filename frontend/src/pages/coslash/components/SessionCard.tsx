import { useState } from 'react';
import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { CopyableBadge } from '@/pages/coslash/components/CopyableBadge';
import { MachineBadge } from '@/pages/coslash/components/MachineBadge';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatDuration, formatEstimatedCost, formatTimeAgo, formatTokens } from '@/pages/coslash/lib/format';
import {
  boardStatusKey,
  displayStatusLabel,
  environmentFact,
  getModality,
  getSessionCardSummary,
  getTotalTokens,
  getVendor,
  STATUSES,
  SUBAGENT_STATUSES,
  sumTokens,
  type Session,
  type Subagent,
  type SubagentCommand,
} from '@/pages/coslash/lib/session';

const SUBAGENT_PREVIEW_COUNT = 3;

export type SessionCardVariant = 'detailed' | 'compact';

export type SessionCardProps = {
  session: Session;
  onClick?: () => void;
  variant?: SessionCardVariant;
  showMachineBadge?: boolean;
};

function shortenSessionId(id: string): string {
  const dashIndex = id.indexOf('-');
  return dashIndex === -1 ? id : id.slice(0, dashIndex);
}

export function SessionVendorBadge({ agent, abbreviated = false }: { agent: string; abbreviated?: boolean }) {
  const vendor = getVendor(agent);
  return (
    <Badge
      title={abbreviated ? vendor.label : undefined}
      className={cn('text-xs', vendor.fg, vendor.bg, {
        'font-mono font-bold': abbreviated,
        'font-semibold': !abbreviated,
      })}
    >
      {abbreviated ? vendor.mono : vendor.label}
    </Badge>
  );
}

export function SessionName({
  name,
  variant = 'detailed',
}: {
  name: string | null;
  variant?: SessionCardVariant | 'inspector';
}) {
  return (
    <span
      className={cn('min-w-0 truncate', {
        'max-w-96 text-sm font-semibold tracking-tight': variant === 'detailed',
        'text-xs font-semibold': variant === 'compact',
        'block text-sm font-bold': variant === 'inspector',
        'text-muted-foreground font-normal': name == null,
        'opacity-75': name == null && variant === 'detailed',
      })}
    >
      {name ?? 'Untitled session'}
    </span>
  );
}

export function SessionId({ id, shortened = false }: { id: string; shortened?: boolean }) {
  return (
    <CopyableBadge
      value={id}
      ariaLabel={`Copy session ID ${id}`}
      copiedLabel="Session ID copied"
      className="text-muted-foreground/80 shrink-0 font-mono text-[11px] font-normal"
    >
      {shortened ? shortenSessionId(id) : id}
    </CopyableBadge>
  );
}

function StatusBadge({ session }: { session: Session }) {
  const key = boardStatusKey(session);
  const status = STATUSES[key];
  const label = displayStatusLabel(session);
  const quiet = key === 'unknown' || key === 'inactive' || key === 'idle';
  if (quiet) {
    return (
      <span className="text-muted-foreground inline-flex shrink-0 items-center gap-1.5 text-xs">
        <span className={cn('size-1.5 shrink-0 rounded-full', status.dot)} />
        {label}
      </span>
    );
  }
  return (
    <Badge
      className={cn('gap-1.5 text-xs font-semibold', status.fg, status.bg, {
        'ring-warning/30 ring-1': key === 'waiting',
      })}
    >
      {!session.displayStale && (
        <span className={cn('size-1.5 rounded-full', status.dot, { 'animate-pulse': key === 'busy' })} />
      )}
      {key === 'waiting' ? 'Waiting on you' : label}
    </Badge>
  );
}

function Modality({ session }: { session: Session }) {
  if (session.entrypoint == null) return null;
  const modality = getModality(session.entrypoint);
  if (modality !== 'Autonomous') return null;
  return (
    <Badge variant="secondary" className="text-muted-foreground text-xs font-semibold">
      {modality}
    </Badge>
  );
}

function TokenUsageAndCost({ session }: { session: Session }) {
  return (
    <div className="flex w-28 shrink-0 flex-col items-end gap-0.5 text-right">
      <div className="text-muted-foreground text-sm tabular-nums">
        <UnpricedModelWarning unpriced={session.unpricedModels}>
          {formatEstimatedCost(session.cost)}
        </UnpricedModelWarning>
      </div>
      <div className="text-muted-foreground/80 font-mono text-[11px] tabular-nums">
        {formatTokens(getTotalTokens(session.tokens))} tok
      </div>
    </div>
  );
}

function CompactSessionCard({ session, showMachineBadge }: { session: Session; showMachineBadge: boolean }) {
  return (
    <>
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <SessionVendorBadge agent={session.agent} abbreviated />
          <SessionName name={session.name} variant="compact" />
          {showMachineBadge && <MachineBadge label={session.sourceLabel} />}
        </div>
        <span className="text-muted-foreground text-xs whitespace-nowrap tabular-nums">
          <UnpricedModelWarning unpriced={session.unpricedModels}>
            {formatEstimatedCost(session.cost)}
          </UnpricedModelWarning>
        </span>
      </div>
      <div className="text-muted-foreground line-clamp-2 text-xs">{getSessionCardSummary(session)}</div>
      <div className="flex items-center justify-between gap-2">
        <SessionId id={session.id} shortened />
        <div className="text-muted-foreground text-right font-mono text-xs">
          {formatTimeAgo(session.mtime)}
        </div>
      </div>
    </>
  );
}

function DetailedSessionCard({ session, showMachineBadge }: { session: Session; showMachineBadge: boolean }) {
  const summary = getSessionCardSummary(session);
  const meta = [
    environmentFact(session.repo),
    environmentFact(session.branch),
    formatTimeAgo(session.mtime),
    formatDuration(session.durationMs),
    session.files > 0 ? `${session.files} files` : null,
  ]
    .filter((part) => part != null && part !== '—')
    .join(' · ');

  return (
    <div
      className={cn('grid grid-cols-[minmax(0,1fr)_6.5rem] items-start gap-x-4 gap-y-1', {
        'opacity-75': session.displayStale,
      })}
    >
      <div className="min-w-0">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <SessionVendorBadge agent={session.agent} />
          <SessionName name={session.name} />
          <StatusBadge session={session} />
          {showMachineBadge && (
            <span className="text-muted-foreground shrink-0 text-xs">{session.sourceLabel}</span>
          )}
          <Modality session={session} />
        </div>
        <div className="text-muted-foreground mt-1 line-clamp-1 text-xs leading-snug">{summary}</div>
        <div
          className="text-muted-foreground/80 mt-0.5 flex min-w-0 items-center gap-2 font-mono text-[11px]"
          title={environmentFact(session.cwd)}
        >
          <span className="min-w-0 truncate">{meta}</span>
          <SessionId id={session.id} shortened />
        </div>
      </div>
      <TokenUsageAndCost session={session} />
    </div>
  );
}

function SubagentBadge() {
  return <Badge className="text-subagent bg-subagent-bg shrink-0 text-xs font-semibold">Subagent</Badge>;
}

export function SubagentModelBadge({ model }: { model: string | null }) {
  return (
    <Badge variant="secondary" className="text-muted-foreground shrink-0 font-mono text-xs">
      {model ?? '—'}
    </Badge>
  );
}

function SubagentStatusBadge({ status }: { status: Subagent['status'] }) {
  const style = SUBAGENT_STATUSES[status];
  return <Badge className={cn('shrink-0 text-xs font-semibold', style.fg, style.bg)}>{style.label}</Badge>;
}

// Cache writes fold the 5-minute and 1-hour buckets into one figure.
export function TokenBreakdown({ tokens }: { tokens: Session['tokens'] }) {
  return (
    <div className="text-muted-foreground pt-1">
      in {formatTokens(sumTokens(tokens, 'input_tokens'))} · out{' '}
      {formatTokens(sumTokens(tokens, 'output_tokens'))} · cache{' '}
      {formatTokens(sumTokens(tokens, 'cache_read_input_tokens'))}r /{' '}
      {formatTokens(
        sumTokens(tokens, 'cache_creation_input_tokens') +
          sumTokens(tokens, 'cache_creation_1h_input_tokens'),
      )}
      w
    </div>
  );
}

function SubagentTokenSummary({ subagent }: { subagent: Subagent }) {
  return (
    <div className="bg-muted rounded-lg border p-2 font-mono text-xs">
      <div className="flex flex-wrap items-baseline justify-between gap-1">
        <span className="text-muted-foreground">
          {formatDuration(subagent.durationMs)} · {subagent.toolUses} tools ·{' '}
          {formatTokens(getTotalTokens(subagent.tokens))} tok
        </span>
        <span className="font-bold">{formatEstimatedCost(subagent.cost)}</span>
      </div>
      <TokenBreakdown tokens={subagent.tokens} />
    </div>
  );
}

function SubagentCommands({ commands }: { commands: SubagentCommand[] }) {
  if (commands.length === 0) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="text-xs font-bold tracking-widest">Steps</div>
      <div className="bg-muted max-h-44 overflow-auto rounded-lg border p-3">
        <div className="flex w-max flex-col gap-1">
          {commands.map(({ label, command }, index) => (
            <div
              key={index}
              className="text-muted-foreground flex gap-2 font-mono text-xs whitespace-nowrap"
              title={command}
            >
              <span>·</span>
              <span>{label}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function SubagentProse({
  label,
  labelClass,
  text,
  italic = false,
}: {
  label: string;
  labelClass: string;
  text: string;
  italic?: boolean;
}) {
  if (text === '') return null;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className={cn('text-xs font-bold tracking-widest', labelClass)}>{label}</div>
      <div className={cn('bg-muted max-h-44 overflow-auto rounded-lg border p-3 text-xs', { italic })}>
        <div className="wrap-break-word whitespace-pre-wrap">{text}</div>
      </div>
    </div>
  );
}

export function SubagentDialogContent({
  subagent,
  parentName,
}: {
  subagent: Subagent;
  parentName: string | null;
}) {
  return (
    <DialogContent className="sm:max-w-2xl">
      <DialogHeader className="min-w-0">
        <div className="flex min-w-0 items-center gap-2 pr-8">
          <SubagentBadge />
          <DialogTitle className="min-w-0 flex-1 truncate text-base">{subagent.name}</DialogTitle>
        </div>
        <DialogDescription asChild>
          <div className="flex flex-col gap-2 pt-1">
            <div className="text-muted-foreground text-xs">
              Subagents run in their own context window and return one result to the parent — they aren't
              resumed on their own.
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-muted-foreground text-xs">
                Spawned by{' '}
                <span className="text-foreground font-semibold">{parentName ?? 'Untitled session'}</span>
                {subagent.spawnedAtTurn != null && ` at turn ${subagent.spawnedAtTurn}`}
              </span>
              <SubagentStatusBadge status={subagent.status} />
              <SubagentModelBadge model={subagent.model} />
            </div>
          </div>
        </DialogDescription>
      </DialogHeader>
      <SubagentTokenSummary subagent={subagent} />
      <SubagentProse label="Task" labelClass="text-subagent" text={subagent.task} italic />
      <SubagentCommands commands={subagent.commands} />
      <SubagentProse label="Result" labelClass="text-success-fg" text={subagent.result} />
    </DialogContent>
  );
}

function DetailedSubagentRow({ subagent }: { subagent: Subagent }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <SubagentBadge />
        <span className="min-w-0 truncate text-xs font-semibold">{subagent.name}</span>
        <SubagentModelBadge model={subagent.model} />
        <SubagentStatusBadge status={subagent.status} />
      </div>
      <div className="flex flex-none items-center gap-2">
        <span className="text-xs font-light whitespace-nowrap">{formatEstimatedCost(subagent.cost)}</span>
        <span className="text-muted-foreground font-mono text-xs whitespace-nowrap">
          {formatTokens(getTotalTokens(subagent.tokens))} tok
        </span>
      </div>
    </div>
  );
}

function CompactSubagentRow({ subagent }: { subagent: Subagent }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex min-w-0 items-center gap-2">
        <SubagentBadge />
        <span className="min-w-0 truncate text-xs font-semibold">{subagent.name}</span>
      </div>
      <span className="text-xs font-light whitespace-nowrap">{formatEstimatedCost(subagent.cost)}</span>
    </div>
  );
}

function SubagentCard({
  subagent,
  parentName,
  variant,
}: {
  subagent: Subagent;
  parentName: string | null;
  variant: SessionCardVariant;
}) {
  const compact = variant === 'compact';
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Card
          size={compact ? 'sm' : 'default'}
          className={cn('bg-subagent-card cursor-pointer', compact ? 'px-3' : 'px-4')}
        >
          {compact ? <CompactSubagentRow subagent={subagent} /> : <DetailedSubagentRow subagent={subagent} />}
        </Card>
      </DialogTrigger>
      <SubagentDialogContent subagent={subagent} parentName={parentName} />
    </Dialog>
  );
}

function SubagentExpandToggle({
  hiddenCount,
  expanded,
  onToggle,
}: {
  hiddenCount: number;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      className="text-subagent flex w-fit cursor-pointer items-center gap-1 text-xs font-semibold"
      onClick={(event) => {
        event.stopPropagation();
        onToggle();
      }}
    >
      {expanded ? <ChevronDownIcon className="size-3" /> : <ChevronRightIcon className="size-3" />}
      {expanded ? 'Show less' : `Show ${hiddenCount} more`}
    </button>
  );
}

function SessionSubagentRail({
  subagents,
  parentName,
  variant,
}: {
  subagents: Subagent[];
  parentName: string | null;
  variant: SessionCardVariant;
}) {
  const [expanded, setExpanded] = useState(false);
  if (subagents.length === 0) return null;

  if (variant === 'detailed' && !expanded) {
    return (
      <div className="px-3 pb-1.5 pl-11">
        <button
          type="button"
          className="text-muted-foreground hover:text-subagent flex w-fit cursor-pointer items-center gap-1.5 rounded-md px-1.5 py-0.5 text-xs font-medium transition-colors"
          onClick={(event) => {
            event.stopPropagation();
            setExpanded(true);
          }}
        >
          <ChevronRightIcon className="size-3" />
          <span className="bg-subagent-bg text-subagent rounded-md px-1.5 py-0.5 font-semibold">
            {subagents.length} {subagents.length === 1 ? 'subagent' : 'subagents'}
          </span>
        </button>
      </div>
    );
  }

  const visible = expanded ? subagents : subagents.slice(0, SUBAGENT_PREVIEW_COUNT);
  const hiddenCount = subagents.length - visible.length;

  return (
    <div className={cn(variant === 'compact' ? 'pl-3' : 'px-3 pb-2 pl-11')}>
      <div className="border-subagent-rail flex flex-col gap-1.5 border-l-2 pl-3">
        {variant === 'detailed' && (
          <button
            type="button"
            className="text-muted-foreground hover:text-foreground flex w-fit cursor-pointer items-center gap-1 text-xs font-medium"
            onClick={(event) => {
              event.stopPropagation();
              setExpanded(false);
            }}
          >
            <ChevronDownIcon className="size-3" />
            Hide subagents
          </button>
        )}
        {visible.map((subagent) => (
          <SubagentCard key={subagent.id} subagent={subagent} parentName={parentName} variant={variant} />
        ))}
        {variant === 'compact' && (hiddenCount > 0 || expanded) && (
          <SubagentExpandToggle
            hiddenCount={hiddenCount}
            expanded={expanded}
            onToggle={() => setExpanded(!expanded)}
          />
        )}
      </div>
    </div>
  );
}

export function SessionCard({
  session,
  onClick,
  variant = 'detailed',
  showMachineBadge = false,
}: SessionCardProps) {
  if (variant === 'compact') {
    return (
      <div className="flex flex-col gap-1.5">
        <Card className="cursor-pointer gap-1 p-3" onClick={onClick}>
          <CompactSessionCard session={session} showMachineBadge={showMachineBadge} />
        </Card>
        <SessionSubagentRail subagents={session.subagents} parentName={session.name} variant={variant} />
      </div>
    );
  }

  return (
    <div className="hover:bg-muted/40 border-border/70 group border-b transition-colors">
      <div
        role="button"
        tabIndex={0}
        className="cursor-pointer px-3 py-2.5"
        onClick={onClick}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return;
          event.preventDefault();
          onClick?.();
        }}
      >
        <DetailedSessionCard session={session} showMachineBadge={showMachineBadge} />
      </div>
      <SessionSubagentRail subagents={session.subagents} parentName={session.name} variant={variant} />
    </div>
  );
}
