import { Badge } from '@/components/ui/badge';
import { DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { CopyableBadge } from '@/pages/coslash/components/CopyableBadge';
import { formatDuration, formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import {
  getTotalTokens,
  getVendor,
  SUBAGENT_STATUSES,
  sumTokens,
  type Session,
  type Subagent,
  type SubagentCommand,
} from '@/pages/coslash/lib/session';

function shortenSessionId(id: string): string {
  const dashIndex = id.indexOf('-');
  return dashIndex === -1 ? id : id.slice(0, dashIndex);
}

export function SessionVendorBadge({ agent }: { agent: string }) {
  const vendor = getVendor(agent);
  return <Badge className={cn('text-xs font-semibold', vendor.fg, vendor.bg)}>{vendor.label}</Badge>;
}

export function SessionName({ name }: { name: string | null }) {
  return (
    <span
      className={cn('block min-w-0 truncate text-sm font-bold', {
        'text-coslash-muted font-normal': name == null,
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
      className="text-coslash-muted font-mono text-xs"
    >
      {shortened ? shortenSessionId(id) : id}
    </CopyableBadge>
  );
}

function SubagentBadge() {
  return <Badge className="text-subagent bg-subagent-bg shrink-0 text-xs font-semibold">Subagent</Badge>;
}

export function SubagentModelBadge({ model }: { model: string | null }) {
  return (
    <Badge variant="secondary" className="text-coslash-muted shrink-0 font-mono text-xs">
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
  if (Object.keys(tokens).length === 0) {
    return <div className="text-coslash-muted pt-1">—</div>;
  }
  return (
    <div className="text-coslash-muted pt-1">
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
    <div className="bg-coslash-soft rounded-lg border p-2 font-mono text-xs">
      <div className="flex flex-wrap items-baseline justify-between gap-1">
        <span className="text-coslash-muted">
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
      <div className="bg-coslash-soft max-h-44 overflow-auto rounded-lg border p-3">
        <div className="flex w-max flex-col gap-1">
          {commands.map(({ label, command }, index) => (
            <div
              key={index}
              className="text-coslash-muted flex gap-2 font-mono text-xs whitespace-nowrap"
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
      <div className={cn('bg-coslash-soft max-h-44 overflow-auto rounded-lg border p-3 text-xs', { italic })}>
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
    <DialogContent className="coslash-shell sm:max-w-2xl">
      <DialogHeader className="min-w-0">
        <div className="flex min-w-0 items-center gap-2 pr-8">
          <SubagentBadge />
          <DialogTitle className="min-w-0 flex-1 truncate text-base">{subagent.name}</DialogTitle>
        </div>
        <DialogDescription asChild>
          <div className="flex flex-col gap-2 pt-1">
            <div className="text-coslash-muted text-xs">
              Subagents run in their own context window and return one result to the parent — they aren't
              resumed on their own.
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-coslash-muted text-xs">
                Spawned by{' '}
                <span className="text-coslash-ink font-semibold">{parentName ?? 'Untitled session'}</span>
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
