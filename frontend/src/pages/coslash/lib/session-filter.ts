import { locationChoiceId, matchingLocation, type SessionGroups } from '@/pages/coslash/lib/detected-groups';
import { boardStatusKey, sessionCost, sessionDisplayTitle, type Session } from '@/pages/coslash/lib/session';
import type { SessionSort, SessionViewPreferences } from '@/pages/coslash/lib/session-view-preferences';

export type FacetKey = 'status' | 'machine' | 'agent' | 'group';

export function matchesFacets(
  session: Session,
  groups: SessionGroups,
  preferences: SessionViewPreferences,
  skip?: FacetKey,
): boolean {
  const { statusFilters, groupFilters, machineFilters, agentFilters } = preferences;
  if (skip !== 'status' && statusFilters.length > 0 && !statusFilters.includes(boardStatusKey(session)))
    return false;
  if (skip !== 'group' && groupFilters.length > 0 && !matchingLocation(groups, groupFilters)) return false;
  if (skip !== 'machine' && machineFilters.length > 0 && !machineFilters.includes(session.sourceId))
    return false;
  if (skip !== 'agent' && agentFilters.length > 0 && !agentFilters.includes(session.agent)) return false;
  return true;
}

/** Each facet counts the sessions that pass every other facet. */
export function countFacets(
  sessions: Session[],
  groupsOf: (session: Session) => SessionGroups,
  preferences: SessionViewPreferences,
): Record<FacetKey, Map<string, number>> {
  const valueOf: Record<Exclude<FacetKey, 'group'>, (session: Session) => string> = {
    status: (session) => boardStatusKey(session),
    machine: (session) => session.sourceId,
    agent: (session) => session.agent,
  };
  const counts = {} as Record<FacetKey, Map<string, number>>;
  for (const facet of [...Object.keys(valueOf), 'group'] as FacetKey[]) {
    const facetCount = new Map<string, number>();
    for (const session of sessions) {
      const groups = groupsOf(session);
      if (!matchesFacets(session, groups, preferences, facet)) continue;
      if (facet === 'group') {
        const counted = new Set<string>();
        for (const option of groups.options) {
          // A repository name alone doesn't verify that a session used its main checkout.
          if (option.kind !== 'Repository') counted.add(locationChoiceId(option));
        }
        for (const id of counted) facetCount.set(id, (facetCount.get(id) ?? 0) + 1);
      } else {
        const value = valueOf[facet](session);
        facetCount.set(value, (facetCount.get(value) ?? 0) + 1);
      }
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
