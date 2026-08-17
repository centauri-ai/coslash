import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { assertOneOf } from '@/pages/coslash/lib/narrow';
import { getStatus, getTotalTokens, type Session, type StatusKey } from '@/pages/coslash/lib/session';

export const SortKey = {
  Recency: 'recency',
  Status: 'status',
  Value: 'value',
  Tokens: 'tokens',
  Duration: 'duration',
} as const;
export type SortKey = (typeof SortKey)[keyof typeof SortKey];

export type SortDir = 'asc' | 'desc';

const SORT_LABELS: Record<SortKey, string> = {
  [SortKey.Recency]: 'Recency',
  [SortKey.Status]: 'Status',
  [SortKey.Value]: 'Est. cost',
  [SortKey.Tokens]: 'Tokens',
  [SortKey.Duration]: 'Duration',
};

const STATUS_PRIORITY: Record<StatusKey, number> = {
  busy: 3,
  idle: 2,
  waiting: 1,
  inactive: 0,
};

function sortValue(session: Session, key: SortKey): number {
  switch (key) {
    case SortKey.Recency:
      return session.mtime;
    case SortKey.Status:
      return STATUS_PRIORITY[getStatus(session.status)];
    case SortKey.Value:
      return session.cost;
    case SortKey.Tokens:
      return getTotalTokens(session.tokens);
    case SortKey.Duration:
      return session.durationMs ?? 0;
  }
}

export function sortSessions(sessions: Session[], key: SortKey, dir: SortDir): Session[] {
  return [...sessions].sort((a, b) => {
    const diff = sortValue(a, key) - sortValue(b, key);
    return dir === 'desc' ? -diff : diff;
  });
}

export function SessionSortDropdownMenu({
  sortKey,
  sortDir,
  onSortKeyChange,
  onSortDirChange,
}: {
  sortKey: SortKey;
  sortDir: SortDir;
  onSortKeyChange: (key: SortKey) => void;
  onSortDirChange: (dir: SortDir) => void;
}) {
  const arrow = sortDir === 'desc' ? '↓' : '↑';

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="text-xs font-semibold">
          Sort: {SORT_LABELS[sortKey]}
          <span className="text-brand font-bold">{arrow}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuRadioGroup
          value={sortKey}
          onValueChange={(key) => onSortKeyChange(assertOneOf(key, Object.values(SortKey)))}
        >
          {Object.entries(SORT_LABELS).map(([key, label]) => (
            <DropdownMenuRadioItem key={key} value={key} className="text-xs font-semibold">
              <span>{label}</span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="justify-between text-xs font-semibold"
          onSelect={(event) => {
            event.preventDefault();
            onSortDirChange(sortDir === 'desc' ? 'asc' : 'desc');
          }}
        >
          <span>Direction</span>
          <span className="text-brand font-bold">{arrow}</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
