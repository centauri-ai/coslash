import type { TimeWindow } from '@/pages/coslash/lib/time-window';

export function sessionsEmptyStateCopy({
  hasSessions,
  searchTerm,
  timeWindow,
}: {
  hasSessions: boolean;
  searchTerm: string;
  timeWindow: TimeWindow;
}): { kind: 'first-run' } | { kind: 'copy'; title: string; detail?: string } {
  if (!hasSessions) {
    return timeWindow === 'all'
      ? { kind: 'first-run' }
      : {
          kind: 'copy',
          title: 'No sessions in this window.',
          detail: 'Select “All” to view older sessions.',
        };
  }

  const term = searchTerm.trim();
  if (term !== '') {
    return {
      kind: 'copy',
      title: `Nothing matches “${term}” in this window.`,
      detail: timeWindow === 'all' ? undefined : 'Select “All” to search older sessions.',
    };
  }

  return { kind: 'copy', title: 'No sessions match these filters.' };
}
