import {
  isEligibleForSharing,
  sessionLogicalId,
  sessionRevision,
  sessionShareEligibility,
  type Session,
  type ShareEligibility,
  type SourceClass,
} from './session';

export type SessionLibraryFilters = {
  search: string;
  repository: string;
  source: 'all' | SourceClass;
  shareState: 'all' | ShareEligibility;
};

export const ALL_REPOSITORIES = 'all-repositories';

/**
 * A source may return a cached older revision beside a freshly collected
 * revision. Keep exactly one deterministic row per logical session, without
 * treating similarly named sessions on distinct sources as the same work.
 */
export function latestLogicalSessions<T extends Session>(sessions: readonly T[]): T[] {
  const latest = new Map<string, T>();
  for (const session of sessions) {
    const key = sessionLogicalId(session);
    const prior = latest.get(key);
    if (prior == null || sessionRevision(session) > sessionRevision(prior)) latest.set(key, session);
  }
  return [...latest.values()];
}

export function libraryRepositories(sessions: readonly Pick<Session, 'repo'>[]): string[] {
  return [...new Set(sessions.flatMap((session) => (session.repo?.trim() ? [session.repo] : [])))].sort(
    (a, b) => a.localeCompare(b),
  );
}

export function filterSessionLibrary<T extends Session>(
  sessions: readonly T[],
  filters: SessionLibraryFilters,
): T[] {
  const search = filters.search.trim().toLowerCase();
  return sessions.filter((session) => {
    if (filters.repository !== ALL_REPOSITORIES && session.repo !== filters.repository) return false;
    if (filters.source !== 'all' && session.sourceClass !== filters.source) return false;
    if (filters.shareState !== 'all' && sessionShareEligibility(session) !== filters.shareState) return false;
    if (!search) return true;
    // Deliberately exclude cwd and source IDs: both may reveal local-only or
    // operational metadata. Repository, branch, title, and agent are the
    // searchable library fields.
    return [session.name, session.repo, session.branch, session.agent].some(
      (value) => value != null && value.toLowerCase().includes(search),
    );
  });
}

/** Stable, source-neutral handoff for LB-04's explicit-review workflow. */
export function eligibleSessionCandidates<T extends Session>(sessions: readonly T[]): T[] {
  return latestLogicalSessions(sessions).filter(isEligibleForSharing);
}
