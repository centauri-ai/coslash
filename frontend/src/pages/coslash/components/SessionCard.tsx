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
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatDuration, formatEstimatedCost, formatTimeAgo, formatTokens } from '@/pages/coslash/lib/format';
import { getEstimatedCost } from '@/pages/coslash/lib/pricing';
import {
  getModality,
  getSessionCardSummary,
  getStatus,
  getTotalTokens,
  getVendor,
  STATUSES,
  SUBAGENT_STATUSES,
  sumTokens,
  type Session,
  type Status,
  type Subagent,
  type SubagentCommand,
} from '@/pages/coslash/lib/session';

const SUBAGENT_PREVIEW_COUNT = 3;

export type SessionCardVariant = 'detailed' | 'compact';

export type SessionCardProps = {
  session: Session;
  onClick?: () => void;
  variant?: SessionCardVariant;
};

function shortenSessionId(id: string): string {
  const dashIndex = id.indexOf('-');
  return dashIndex === -1 ? id : id.slice(0, dashIndex);
}

export function SessionVendorBadge({ agent, abbreviated = false }: { agent: string; abbreviated?: boolean }) {
  const vendor = getVendor(agent);
  return (
    <Badge
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
        'max-w-80 text-sm font-semibold': variant === 'detailed',
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
      ariaLabel={`Copy session UUID ${id}`}
      copiedLabel="UUID copied"
      className="text-muted-foreground font-mono text-xs"
    >
      {shortened ? shortenSessionId(id) : id}
    </CopyableBadge>
  );
}

function StatusBadge({ status }: { status: Status }) {
  return (
    <Badge className={cn('gap-1 text-xs font-semibold', status.fg, status.bg)}>
      <span className={cn('size-1 rounded-full', status.dot)} />
      {status.label}
    </Badge>
  );
}

function Modality({ session }: { session: Session }) {
  if (session.entrypoint == null) return null;
  return (
    <Badge variant="secondary" className="text-muted-foreground text-xs font-semibold">
      {getModality(session.entrypoint)}
    </Badge>
  );
}

function Metadata({ session }: { session: Session }) {
  return (
    <div className="text-muted-foreground pt-2 font-mono text-xs" title={session.cwd}>
      {session.repo ?? '—'} · {session.branch ?? '—'} · {formatTimeAgo(session.mtime)} ·{' '}
      {formatDuration(session.durationMs)} · {session.files} files
    </div>
  );
}

function TokenUsageAndCost({ session }: { session: Session }) {
  return (
    <div className="flex-none text-right">
      <div className="text-base font-bold">
        <UnpricedModelWarning tokens={[session.tokens]}>
          {formatEstimatedCost(getEstimatedCost(session.tokens))}
        </UnpricedModelWarning>
      </div>
      <div className="text-muted-foreground pt-1 font-mono text-xs">
        {formatTokens(getTotalTokens(session.tokens))} tok
      </div>
    </div>
  );
}

function Summary({ session }: { session: Session }) {
  return <div className="text-muted-foreground pt-2 text-xs">{getSessionCardSummary(session)}</div>;
}

function CompactSessionCard({ session }: { session: Session }) {
  return (
    <>
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <SessionVendorBadge agent={session.agent} abbreviated />
          <SessionName name={session.name} variant="compact" />
        </div>
        <span className="text-xs font-semibold whitespace-nowrap">
          <UnpricedModelWarning tokens={[session.tokens]}>
            {formatEstimatedCost(getEstimatedCost(session.tokens))}
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

function DetailedSessionCard({ session }: { session: Session }) {
  const status = STATUSES[getStatus(session.status)];

  return (
    <div className="flex items-start gap-4">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <SessionVendorBadge agent={session.agent} />
          <SessionName name={session.name} />
          <SessionId id={session.id} />
          <StatusBadge status={status} />
          <Modality session={session} />
        </div>
        <Summary session={session} />
        <Metadata session={session} />
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
        <span className="font-bold">{formatEstimatedCost(getEstimatedCost(subagent.tokens))}</span>
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
        <span className="text-xs font-light whitespace-nowrap">
          {formatEstimatedCost(getEstimatedCost(subagent.tokens))}
        </span>
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
      <span className="text-xs font-light whitespace-nowrap">
        {formatEstimatedCost(getEstimatedCost(subagent.tokens))}
      </span>
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

  const visible = expanded ? subagents : subagents.slice(0, SUBAGENT_PREVIEW_COUNT);
  const hiddenCount = subagents.length - visible.length;

  return (
    <div className={cn('flex flex-col', variant === 'compact' ? 'pl-3' : 'pl-20')}>
      <div className="border-subagent-rail flex flex-col gap-2 border-l-3 pl-4">
        {visible.map((subagent) => (
          <SubagentCard key={subagent.id} subagent={subagent} parentName={parentName} variant={variant} />
        ))}
        {(hiddenCount > 0 || expanded) && (
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

export function SessionCard({ session, onClick, variant = 'detailed' }: SessionCardProps) {
  return (
    <div className="flex flex-col gap-2">
      <Card className={cn('cursor-pointer', variant === 'compact' ? 'gap-1 p-3' : 'p-4')} onClick={onClick}>
        {variant === 'compact' ? (
          <CompactSessionCard session={session} />
        ) : (
          <DetailedSessionCard session={session} />
        )}
      </Card>
      <SessionSubagentRail subagents={session.subagents} parentName={session.name} variant={variant} />
    </div>
  );
}
