import { Badge } from '@/components/ui/badge';
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { CopyableBadge } from '@/pages/coslash/components/CopyableBadge';
import { MachineBadge } from '@/pages/coslash/components/MachineBadge';
import {
  SessionId,
  SessionName,
  SessionVendorBadge,
  TokenBreakdown,
} from '@/pages/coslash/components/SessionCard';
import { SynthesisCosts } from '@/pages/coslash/components/SynthesisCosts';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatDuration, formatEstimatedCost } from '@/pages/coslash/lib/format';
import {
  boardStatusKey,
  displayStatusLabel,
  getModality,
  getVendor,
  sessionCost,
  sessionKey,
  sessionLocationFact,
  STATUSES,
  type SessionDetail,
} from '@/pages/coslash/lib/session';

export function SessionModelUsage({
  agent,
  model,
  observedModels,
  tokens,
}: Pick<SessionDetail, 'agent' | 'model' | 'observedModels' | 'tokens'>) {
  const vendor = getVendor(agent);
  if (model == null) return <span className={cn('font-bold', vendor.fg)}>unknown model</span>;

  const otherModels = [...new Set([...(observedModels ?? []), ...Object.keys(tokens)])].filter(
    (tokenModel) => tokenModel !== model,
  );
  if (otherModels.length === 0) return <span className={cn('font-bold', vendor.fg)}>{model}</span>;

  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex items-center gap-1">
        {otherModels.map((otherModel) => (
          <span key={otherModel} aria-hidden="true" className="bg-coslash-neutral-dot size-2 rounded-full" />
        ))}
        <span aria-hidden="true" className={cn('size-2 rounded-full bg-current', vendor.fg)} />
      </span>
      <span className={cn('font-bold', vendor.fg)}>{model}</span>
      <span className="text-coslash-muted">
        {otherModels.length === 1 ? `after ${otherModels[0]}` : `with ${otherModels.length} other models`}
      </span>
    </span>
  );
}

export function PiErrorBadge({ diagnostic }: { diagnostic?: string | null }) {
  if (!diagnostic) return null;
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge asChild variant="destructive">
            <button type="button" aria-label={`Pi error: ${diagnostic}`}>
              Error
            </button>
          </Badge>
        </TooltipTrigger>
        <TooltipContent className="coslash-shell max-h-48 max-w-[min(24rem,var(--radix-tooltip-content-available-width))] overflow-y-auto break-words whitespace-pre-wrap">
          {diagnostic}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function HeaderMeta({
  detail,
  showMachineBadge,
  synthesisCostVersion,
  synthesisSettledCount,
}: {
  detail: SessionDetail;
  showMachineBadge: boolean;
  synthesisCostVersion: string | null;
  synthesisSettledCount: number;
}) {
  const status = STATUSES[boardStatusKey(detail)];

  return (
    <div className="flex flex-col gap-2 pt-2">
      <div className="text-coslash-muted flex flex-wrap items-center justify-between gap-x-2 gap-y-1 font-mono text-xs">
        <div className="flex min-w-40 flex-1 items-center gap-1 overflow-hidden">
          <Badge variant="secondary">{sessionLocationFact(detail)}</Badge>
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
          {displayStatusLabel(detail)} · {getModality(detail.entrypoint, detail.agent)}
          <PiErrorBadge diagnostic={detail.agentError} />
        </span>
      </div>
      <div className="bg-coslash-soft rounded-lg border p-2 font-mono text-xs">
        <div className="flex flex-wrap items-baseline justify-between gap-1">
          <SessionModelUsage
            agent={detail.agent}
            model={detail.model}
            observedModels={detail.observedModels}
            tokens={detail.tokens}
          />
          <span className="font-bold">
            <UnpricedModelWarning unpriced={detail.unpricedModels}>
              {formatEstimatedCost(sessionCost(detail))}
            </UnpricedModelWarning>
          </span>
        </div>
        <div className="text-coslash-muted pt-1">
          {formatDuration(detail.durationMs)} · {detail.turns} turns · {detail.toolUses} tools ·{' '}
          {detail.errors} errors
        </div>
        <TokenBreakdown {...detail} />
      </div>
      <SynthesisCosts
        key={sessionKey(detail)}
        session={detail}
        codingCost={detail.cost}
        codingUnpricedModels={detail.unpricedModels}
        synthesisCostVersion={synthesisCostVersion}
        synthesisSettledCount={synthesisSettledCount}
      />
    </div>
  );
}

export function SessionInspectorTitle({
  detail,
  showMachineBadge,
}: {
  detail: Pick<SessionDetail, 'agent' | 'id' | 'name' | 'firstPrompt' | 'sourceLabel'>;
  showMachineBadge: boolean;
}) {
  return (
    <div className="flex items-center gap-2 pr-10">
      <SessionVendorBadge agent={detail.agent} />
      <div className="min-w-0 flex-1">
        <SessionName session={detail} />
      </div>
      <SessionId id={detail.id} shortened />
      {showMachineBadge && <MachineBadge label={detail.sourceLabel} />}
    </div>
  );
}
