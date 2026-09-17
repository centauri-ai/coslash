import {
  isEligibleForSharing,
  isLocalSession,
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

const sessionSearchDocuments = new WeakMap<Session, string>();
const searchableDigestCategories = new Set(['first_prompt', 'user', 'question', 'recap']);

function sessionSearchDocument(session: Session): string {
  const cached = sessionSearchDocuments.get(session);
  if (cached != null) return cached;

  const fields: (string | null | undefined)[] = [session.name, session.repo, session.branch, session.agent];
  if (isLocalSession(session)) {
    fields.push(session.firstPrompt, session.summary, session.declaredGoal);
    for (const entry of session.digest) {
      if (searchableDigestCategories.has(entry.category)) {
        fields.push(entry.description, entry.answer);
      }
    }
    if (session.synthesis != null) {
      fields.push(
        ...session.synthesis.goals,
        session.synthesis.outcome,
        ...session.synthesis.keyDecisions,
        session.synthesis.nextStep,
      );
    }
  }

  const document = fields
    .filter((value): value is string => value != null)
    .join('\n')
    .toLowerCase();
  sessionSearchDocuments.set(session, document);
  return document;
}

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
    return sessionSearchDocument(session).includes(search);
  });
}

/** Stable, source-neutral handoff for LB-04's explicit-review workflow. */
export function eligibleSessionCandidates<T extends Session>(sessions: readonly T[]): T[] {
  return latestLogicalSessions(sessions).filter(isEligibleForSharing);
}
