import { type TimeWindow } from '@/pages/coslash/lib/time-window';

const TIME_WINDOW_SCOPE_LABELS: Record<TimeWindow, string> = {
  'week': 'this week',
  'month': 'this month',
  '7d': 'in the last 7 days',
  '30d': 'in the last 30 days',
  'all': 'across all time',
};

export function sessionsFooterText({
  count,
  timeWindow,
  isLoading,
  loadFailed,
}: {
  count: number;
  timeWindow: TimeWindow;
  isLoading: boolean;
  loadFailed: boolean;
}): string {
  if (loadFailed) return 'Session count unavailable';
  if (isLoading) return 'Loading sessions…';
  return `${count} ${count === 1 ? 'session' : 'sessions'} ${TIME_WINDOW_SCOPE_LABELS[timeWindow]}`;
}

export function sessionsEmptyStateCopy({
  hasSessions,
  searchTerm,
  outsideWindowMatches,
}: {
  hasSessions: boolean;
  searchTerm: string;
  outsideWindowMatches: number;
}): { title: string; detail?: string } {
  if (!hasSessions) return { title: 'No sessions yet.' };

  const term = searchTerm.trim();
  if (term !== '') {
    return {
      title: `Nothing matches “${term}” in this window.`,
      detail:
        outsideWindowMatches > 0
          ? `${outsideWindowMatches} matching ${outsideWindowMatches === 1 ? 'session' : 'sessions'} outside this window.`
          : undefined,
    };
  }

  return { title: 'No sessions match these filters.' };
}
