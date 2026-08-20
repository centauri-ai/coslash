import { Fragment } from 'react';
import { cn } from '@/lib/utils';
import { SessionCard } from '@/pages/coslash/components/SessionCard';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import {
  getStatus,
  getTotalTokens,
  sessionKey,
  sessionsForAggregates,
  STATUS_ORDER,
  STATUSES,
  type Session,
  type Status,
} from '@/pages/coslash/lib/session';

// Preserves insertion order, so sorted input yields groups ordered by their top session.
function groupBy(sessions: Session[], keyOf: (session: Session) => string): [string, Session[]][] {
  const groups = new Map<string, Session[]>();
  for (const session of sessions) {
    const key = keyOf(session);
    let group = groups.get(key);
    if (!group) {
      groups.set(key, [session]);
      continue;
    }
    group.push(session);
  }
  return [...groups.entries()];
}

function StatusColumnHeader({ status, sessions }: { status: Status; sessions: Session[] }) {
  const count = sessionsForAggregates(sessions).length;
  return (
    <div className="bg-background sticky top-0 z-10 flex items-center gap-2 border-b border-l px-3 py-2">
      <span className={cn('size-2 rounded-full', status.dot)} />
      <span className={cn('text-xs font-semibold', status.fg)}>{status.label}</span>
      <span className="text-muted-foreground font-mono text-xs">{count}</span>
    </div>
  );
}

function GroupTotals({ sessions }: { sessions: Session[] }) {
  const aggregate = sessionsForAggregates(sessions);
  const tokens = aggregate.reduce((sum, session) => sum + getTotalTokens(session.tokens), 0);
  const cost = aggregate.reduce((sum, session) => sum + session.cost, 0);

  return (
    <span className="text-muted-foreground text-xs">
      {aggregate.length} {aggregate.length === 1 ? 'session' : 'sessions'} · {formatTokens(tokens)} tok ·{' '}
      <UnpricedModelWarning unpriced={aggregate.flatMap((session) => session.unpricedModels)}>
        {formatEstimatedCost(cost)}
      </UnpricedModelWarning>
    </span>
  );
}

function RepoGroupHeader({ repo, sessions }: { repo: string; sessions: Session[] }) {
  return (
    <div className="bg-muted col-span-full flex items-center justify-between gap-2 border-b px-4 py-2">
      <span className="font-mono text-sm font-bold">{repo}/</span>
      <GroupTotals sessions={sessions} />
    </div>
  );
}

function SessionCardColumn({
  status,
  sessions,
  onSelectSession,
}: {
  status: string;
  sessions: Session[];
  onSelectSession: (session: Session) => void;
}) {
  return (
    <>
      {sessions
        .filter((session) => getStatus(session.status) === status)
        .map((session) => (
          <SessionCard
            key={sessionKey(session)}
            session={session}
            onClick={() => onSelectSession(session)}
            variant="compact"
          />
        ))}
    </>
  );
}

function BranchRow({
  branch,
  sessions,
  visibleStatuses,
  onSelectSession,
}: {
  branch: string;
  sessions: Session[];
  visibleStatuses: [string, Status][];
  onSelectSession: (session: Session) => void;
}) {
  return (
    <>
      <div className="flex flex-col gap-1 border-b p-4">
        <span className="text-muted-foreground font-mono text-xs break-all">{branch}</span>
        <GroupTotals sessions={sessions} />
      </div>
      {visibleStatuses.map(([status]) => (
        <div key={status} data-status={status} className="flex flex-col gap-2 border-b border-l p-2">
          <SessionCardColumn status={status} sessions={sessions} onSelectSession={onSelectSession} />
        </div>
      ))}
    </>
  );
}

export function SessionBoard({
  sessions,
  onSelectSession,
}: {
  sessions: Session[];
  onSelectSession: (session: Session) => void;
}) {
  const visibleStatuses = STATUS_ORDER.filter((status) =>
    sessions.some((session) => getStatus(session.status) === status),
  ).map((status): [string, Status] => [status, STATUSES[status]]);

  return (
    <div
      className="bg-background grid"
      style={{ gridTemplateColumns: `repeat(${visibleStatuses.length + 1}, minmax(0, 1fr))` }}
    >
      <div className="bg-background sticky top-0 z-10 border-b" />
      {visibleStatuses.map(([key, status]) => (
        <StatusColumnHeader
          key={key}
          status={status}
          sessions={sessions.filter((session) => getStatus(session.status) === key)}
        />
      ))}
      {groupBy(sessions, (session) => session.repo ?? '(no repo)').map(([repo, repoSessions]) => (
        <Fragment key={repo}>
          <RepoGroupHeader repo={repo} sessions={repoSessions} />
          {groupBy(repoSessions, (session) => session.branch ?? '—').map(([branch, branchSessions]) => (
            <BranchRow
              key={branch}
              branch={branch}
              sessions={branchSessions}
              visibleStatuses={visibleStatuses}
              onSelectSession={onSelectSession}
            />
          ))}
        </Fragment>
      ))}
    </div>
  );
}
