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
