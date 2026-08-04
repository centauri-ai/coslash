import type { TimeWindow } from '@/pages/coslash/lib/time-window';

export function sessionsEmptyStateCopy({
  hasSessions,
  searchTerm,
  timeWindow,
}: {
  hasSessions: boolean;
  searchTerm: string;
  timeWindow: TimeWindow;
}): { title: string; detail?: string } {
  if (!hasSessions) {
    return timeWindow === 'all'
      ? { title: 'No sessions yet.' }
      : {
          title: 'No sessions in this window.',
          detail: 'Select “All” to view older sessions.',
        };
  }

  const term = searchTerm.trim();
  if (term !== '') {
    return {
      title: `Nothing matches “${term}” in this window.`,
      detail: timeWindow === 'all' ? undefined : 'Select “All” to search older sessions.',
    };
  }

  return { title: 'No sessions match these filters.' };
}
