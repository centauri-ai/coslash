import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { SessionVendorBadge } from '@/pages/coslash/components/SessionCard';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTimeAgo } from '@/pages/coslash/lib/format';
import {
  boardStatusKey,
  displayStatusLabel,
  environmentFact,
  getSessionCardSummary,
  sessionKey,
  sessionReadiness,
  STATUSES,
  type Session,
} from '@/pages/coslash/lib/session';

const listGrid = 'grid-cols-[minmax(0,1fr)_4.5rem] md:grid-cols-[minmax(0,1fr)_5.5rem_6.25rem_3.5rem_4.5rem]';

function ActivityPill({ session }: { session: Session }) {
  const status = STATUSES[boardStatusKey(session)];
  return (
    <Badge className={cn('gap-1 text-xs font-semibold', status.fg, status.bg)}>
      <span className={cn('size-1 rounded-full', status.dot)} />
      {displayStatusLabel(session)}
    </Badge>
  );
}

function Readiness({ session }: { session: Session }) {
  const readiness = sessionReadiness(session);
  return (
    <span
      className={cn('flex min-w-0 items-center gap-1.5 text-xs font-semibold', {
        'text-success-fg': readiness.key === 'resume',
        'text-warning-fg': readiness.key === 'review',
        'text-destructive': readiness.key === 'fresh',
        'text-muted-foreground': readiness.key === 'unavailable',
      })}
      title={readiness.detail}
    >
      <span
        className={cn('size-1.5 shrink-0 rounded-full', {
          'bg-success': readiness.key === 'resume',
          'bg-warning': readiness.key === 'review',
          'bg-destructive': readiness.key === 'fresh',
          'border-muted-foreground border bg-transparent': readiness.key === 'unavailable',
        })}
      />
      <span className="truncate">{readiness.label}</span>
    </span>
  );
}

function SessionListRow({ session, onClick }: { session: Session; onClick: () => void }) {
  const readiness = sessionReadiness(session);
  const location = `${environmentFact(session.repo)}${session.branch ? ` · ${session.branch}` : ''}`;

  return (
    <button
      type="button"
      className={cn(
        listGrid,
        'hover:bg-muted/70 focus-visible:ring-ring grid w-full items-center gap-2 border-t px-3 py-3 text-left transition-colors first:border-t-0 focus-visible:ring-2 focus-visible:outline-none md:gap-3 md:px-5',
        { 'opacity-75': session.displayStale },
      )}
      onClick={onClick}
      aria-label={`Inspect ${session.name ?? 'Untitled session'}, ${displayStatusLabel(session)}, ${readiness.label}, ${formatEstimatedCost(session.cost)}`}
    >
      <span className="min-w-0">
        <span className="block truncate text-sm font-semibold">{session.name ?? 'Untitled session'}</span>
        <span className="text-muted-foreground block truncate pt-1 text-xs">
          {getSessionCardSummary(session)}
          {session.subagents.length > 0 && (
            <span className="text-subagent pl-2 font-semibold">
              {session.subagents.length} {session.subagents.length === 1 ? 'subagent' : 'subagents'}
            </span>
          )}
        </span>
        <span className="flex min-w-0 flex-wrap items-center gap-1.5 pt-2">
          <SessionVendorBadge agent={session.agent} />
          <ActivityPill session={session} />
          <span className="text-muted-foreground min-w-0 truncate text-xs" title={location}>
            {location}
          </span>
        </span>
      </span>
      <span className="text-muted-foreground hidden truncate text-xs md:block" title={session.sourceLabel}>
        {session.sourceLabel}
      </span>
      <span className="hidden md:block">
        <Readiness session={session} />
      </span>
      <span className="text-muted-foreground hidden text-right text-xs whitespace-nowrap md:block">
        {formatTimeAgo(session.mtime)}
      </span>
      <span className="text-right text-xs font-semibold whitespace-nowrap">
        <UnpricedModelWarning unpriced={session.unpricedModels}>
          {formatEstimatedCost(session.cost)}
        </UnpricedModelWarning>
      </span>
    </button>
  );
}

export function SessionList({
  sessions,
  onSelectSession,
}: {
  sessions: Session[];
  onSelectSession: (session: Session) => void;
}) {
  return (
    <div className="bg-card overflow-hidden rounded-lg border">
      <div
        className={cn(
          listGrid,
          'bg-muted text-muted-foreground hidden items-center gap-3 px-5 py-2.5 text-xs font-semibold tracking-wide uppercase md:grid',
        )}
      >
        <span>Session / matched context</span>
        <span>Machine</span>
        <span>Readiness</span>
        <span className="text-right">Updated</span>
        <span className="text-right">Est. cost</span>
      </div>
      {sessions.map((session) => (
        <SessionListRow
          key={sessionKey(session)}
          session={session}
          onClick={() => onSelectSession(session)}
        />
      ))}
    </div>
  );
}
