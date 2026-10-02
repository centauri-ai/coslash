import { getVendor, sessionCost, sessionsForAggregates, type Session } from './session';
import { latestLogicalSessions } from './session-library';

type Count = { name: string; count: number };

function ranked(counts: Map<string, number>): Count[] {
  return [...counts]
    .map(([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

function count(counts: Map<string, number>, name: string) {
  counts.set(name, (counts.get(name) ?? 0) + 1);
}

function modelName(value: string): string {
  return value.trim().split('/').at(-1)?.trim().toLowerCase() || 'Unknown';
}

export function buildInsights(sessions: Session[], month: Date) {
  const year = month.getFullYear();
  const index = month.getMonth();
  const start = new Date(year, index, 1).getTime();
  const end = new Date(year, index + 1, 1).getTime();
  const days = Array.from({ length: new Date(year, index + 1, 0).getDate() }, (_, day) => ({
    day: day + 1,
    count: 0,
  }));
  const agents = new Map<string, number>();
  const models = new Map<string, number>();
  const repositories = new Map<string, number>();
  let knownCost = 0;
  let unknownCostCount = 0;
  let sessionCount = 0;

  for (const session of sessionsForAggregates(latestLogicalSessions(sessions))) {
    if (session.mtime < start || session.mtime >= end) continue;
    sessionCount++;
    days[new Date(session.mtime).getDate() - 1].count++;
    count(agents, getVendor(session.agent).label);
    const names = Object.keys(session.tokens);
    for (const name of new Set(
      (names.length
        ? names
        : session.observedModels?.length
          ? session.observedModels
          : [session.model ?? 'Unknown']
      ).map(modelName),
    )) {
      count(models, name);
    }
    if (session.repo?.trim()) count(repositories, session.repo.trim());
    const cost = sessionCost(session);
    if (cost != null) knownCost += cost;
    if (cost == null || session.unpricedModels.length > 0) unknownCostCount++;
  }

  return {
    sessionCount,
    agents: ranked(agents),
    models: ranked(models),
    repositories: ranked(repositories).slice(0, 5),
    days,
    knownCost: unknownCostCount ? null : knownCost,
    knownCostSubtotal: knownCost,
    unknownCostCount,
  };
}
