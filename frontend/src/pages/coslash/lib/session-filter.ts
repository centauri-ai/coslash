import type { Group } from '@/pages/coslash/lib/detected-groups';
import { boardStatusKey, sessionCost, sessionDisplayTitle, type Session } from '@/pages/coslash/lib/session';
import type { SessionSort, SessionViewPreferences } from '@/pages/coslash/lib/session-view-preferences';

export type FacetKey = 'status' | 'machine' | 'agent' | 'group';

export function matchesFacets(
  session: Session,
  group: Group,
  preferences: SessionViewPreferences,
  skip?: FacetKey,
): boolean {
  const { statusFilters, groupFilters, machineFilters, agentFilters } = preferences;
  if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(boardStatusKey(session)))
    return false;
  if (skip !== 'group' && groupFilters.length > 0 && !groupFilters.includes(group.id)) return false;
  if (skip !== 'machine' && machineFilters.length > 0 && !machineFilters.includes(session.sourceId))
    return false;
  if (skip !== 'agent' && agentFilters.length > 0 && !agentFilters.includes(session.agent)) return false;
  return true;
}

/** Each facet counts the sessions that pass every other facet. */
export function countFacets(
  sessions: Session[],
  groupOf: (session: Session) => Group,
  preferences: SessionViewPreferences,
): Record<FacetKey, Map<string, number>> {
  const valueOf: Record<FacetKey, (session: Session, group: Group) => string> = {
    status: (session) => boardStatusKey(session),
    machine: (session) => session.sourceId,
    agent: (session) => session.agent,
    group: (_session, group) => group.id,
  };
  const counts = {} as Record<FacetKey, Map<string, number>>;
  for (const facet of Object.keys(valueOf) as FacetKey[]) {
    const facetCount = new Map<string, number>();
    for (const session of sessions) {
      const group = groupOf(session);
      if (!matchesFacets(session, group, preferences, facet)) continue;
      const value = valueOf[facet](session, group);
      facetCount.set(value, (facetCount.get(value) ?? 0) + 1);
    }
    counts[facet] = facetCount;
  }
  return counts;
}

export function sortSessions(sessions: Session[], sort: SessionSort): Session[] {
  return [...sessions].sort((a, b) => {
    // Rows whose liveness is unknown stay below current rows whatever the sort key.
    if (a.displayStale !== b.displayStale) return a.displayStale ? 1 : -1;
    if (sort.key === 'title') {
      const difference = sessionDisplayTitle(a).localeCompare(sessionDisplayTitle(b));
      return sort.dir === 'asc' ? difference : -difference;
    }
    if (sort.key === 'cost') {
      const left = sessionCost(a),
        right = sessionCost(b);
      if (left == null || right == null) return left == null ? (right == null ? 0 : 1) : -1;
      return sort.dir === 'asc' ? left - right : right - left;
    }
    return sort.dir === 'asc' ? a.mtime - b.mtime : b.mtime - a.mtime;
  });
}
