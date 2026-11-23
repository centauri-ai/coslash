import { searchHitKey, type DecisionHit, type SearchMatch } from '@/pages/coslash/lib/search';
import { sessionDisplayTitle, sessionKey, type Session } from '@/pages/coslash/lib/session';

export function SearchMatchLine({ match }: { match: SearchMatch }) {
  const chars = Array.from(match.snippet);
  let end = 0;
  const parts = [];
  for (const [start, stop] of match.ranges ?? []) {
    if (start < end || stop <= start || stop > chars.length) continue;
    parts.push(chars.slice(end, start).join(''));
    parts.push(<mark key={start}>{chars.slice(start, stop).join('')}</mark>);
    end = stop;
  }
  parts.push(chars.slice(end).join(''));
  return (
    <div className="text-coslash-muted truncate px-2 text-xs">
      {match.kind}
      {match.turn ? `, turn ${match.turn}` : ''}: {parts}
    </div>
  );
}

export function SearchDecisions({
  decisions,
  sessions,
  onSelect,
}: {
  decisions: DecisionHit[];
  sessions: Session[];
  onSelect: (session: Session) => void;
}) {
  const byKey = new Map(sessions.map((session) => [sessionKey(session), session]));
  const hits = decisions.filter((hit) => byKey.has(searchHitKey(hit))).slice(0, 5);
  if (!hits.length) return null;
  return (
    <div className="flex min-w-0 flex-col gap-2 py-3" aria-label="Matching decisions">
      <div className="text-xs font-semibold">Matching decisions</div>
      {hits.map((hit, index) => {
        const session = byKey.get(searchHitKey(hit))!;
        return (
          <button
            key={`${searchHitKey(hit)}:${index}`}
            type="button"
            className="border-coslash-line hover:bg-coslash-soft rounded-md border p-3 text-left text-xs break-words"
            onClick={() => onSelect(session)}
          >
            <div>{hit.text}</div>
            <div className="text-coslash-muted pt-1">
              {sessionDisplayTitle(session)} · {new Date(session.mtime).toLocaleDateString()} ·{' '}
              {hit.cited ? 'Cited' : 'No cited turn'}
            </div>
          </button>
        );
      })}
    </div>
  );
}
