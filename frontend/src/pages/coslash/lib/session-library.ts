import { type Session } from './session';

/**
 * A source may return a cached older revision beside a freshly collected
 * revision. Keep exactly one deterministic row per logical session, without
 * treating similarly named sessions on distinct sources as the same work.
 */
export function latestLogicalSessions<T extends Session>(sessions: readonly T[]): T[] {
  const latest = new Map<string, T>();
  for (const session of sessions) {
    const prior = latest.get(session.logicalSessionId);
    if (prior == null || session.revision > prior.revision) latest.set(session.logicalSessionId, session);
  }
  return [...latest.values()];
}
