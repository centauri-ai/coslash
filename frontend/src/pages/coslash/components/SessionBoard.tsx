import { Fragment } from 'react';
import { cn } from '@/lib/utils';
import { SessionCard } from '@/pages/coslash/components/SessionCard';
import { UnpricedModelWarning } from '@/pages/coslash/components/UnpricedModelWarning';
import { formatEstimatedCost, formatTokens } from '@/pages/coslash/lib/format';
import { getEstimatedCost } from '@/pages/coslash/lib/pricing';
import { getStatus, getTotalTokens, STATUSES, type Session, type Status } from '@/pages/coslash/lib/session';

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
  return (
    <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-l border-l-neutral-100 bg-neutral-50 px-3 py-2">
      <span className={cn('size-2 rounded-full', status.dot)} />
      <span className={cn('text-xs font-semibold', status.fg)}>{status.label}</span>
      <span className="text-muted-foreground font-mono text-xs">{sessions.length}</span>
    </div>
  );
}

function RepoGroupHeader({ repo, sessions }: { repo: string; sessions: Session[] }) {
  const tokens = sessions.reduce((sum, session) => sum + getTotalTokens(session.tokens), 0);
  const cost = sessions.reduce((sum, session) => sum + getEstimatedCost(session.tokens), 0);

  return (
    <div className="bg-muted col-span-full flex items-center justify-between gap-2 border-b px-4 py-2">
      <span className="font-mono text-sm font-bold">{repo}/</span>
      <span className="text-muted-foreground text-xs">
        {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'} · {formatTokens(tokens)} tok ·{' '}
        <UnpricedModelWarning tokens={sessions.map((session) => session.tokens)}>
          {formatEstimatedCost(cost)}
        </UnpricedModelWarning>
      </span>
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
            key={`${session.agent}:${session.id}`}
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
  onSelectSession,
}: {
  branch: string;
  sessions: Session[];
  onSelectSession: (session: Session) => void;
}) {
  const tokens = sessions.reduce((sum, session) => sum + getTotalTokens(session.tokens), 0);
  const cost = sessions.reduce((sum, session) => sum + getEstimatedCost(session.tokens), 0);

  return (
    <>
      <div className="flex flex-col gap-1 border-b p-4">
        <span className="text-muted-foreground font-mono text-xs break-all">{branch}</span>
        <span className="text-muted-foreground text-xs">
          {sessions.length} {sessions.length === 1 ? 'session' : 'sessions'} · {formatTokens(tokens)} tok ·{' '}
          <UnpricedModelWarning tokens={sessions.map((session) => session.tokens)}>
            {formatEstimatedCost(cost)}
          </UnpricedModelWarning>
        </span>
      </div>
      {Object.keys(STATUSES).map((status) => (
        <div
          key={status}
          data-status={status}
          className="flex flex-col gap-2 border-b border-l border-l-neutral-100 p-2"
        >
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
  return (
    <div className="grid grid-cols-5 bg-neutral-50">
      <div className="sticky top-0 z-10 border-b bg-neutral-50" />
      {Object.entries(STATUSES).map(([key, status]) => (
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
              onSelectSession={onSelectSession}
            />
          ))}
        </Fragment>
      ))}
    </div>
  );
}
