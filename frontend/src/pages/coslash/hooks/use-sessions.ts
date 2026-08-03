import { useEffect, useRef, useState } from 'react';
import type { Session } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;

export function sessionsRequestPath(timeWindow: TimeWindow, now = new Date()): string {
  const since = timeWindowStart(timeWindow, now);
  return since == null ? '/api/sessions' : `/api/sessions?since=${since}`;
}

export function mergeKnownSessions(
  current: ReadonlyMap<string, Session>,
  loaded: Session[],
): Map<string, Session> {
  const merged = new Map(current);
  for (const session of loaded) merged.set(session.id, session);
  return merged;
}

export function cacheWindowSessions(
  current: ReadonlyMap<TimeWindow, Session[]>,
  timeWindow: TimeWindow,
  loaded: Session[],
): Map<TimeWindow, Session[]> {
  const updated = new Map(current);
  updated.set(timeWindow, loaded);
  return updated;
}

export function useSessions(timeWindow: TimeWindow) {
  const cache = useRef(new Map<TimeWindow, Session[]>());
  const [latest, setLatest] = useState<{ timeWindow: TimeWindow; sessions: Session[] } | null>(null);
  const [knownSessions, setKnownSessions] = useState<ReadonlyMap<string, Session>>(new Map());
  const [isLoading, setIsLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [retryCount, setRetryCount] = useState(0);
  const [sessionsVersion, setSessionsVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    const cached = cache.current.get(timeWindow);

    const load = (background: boolean) => {
      if (!background) {
        setIsLoading(true);
        setLoadFailed(false);
      }
      fetch(sessionsRequestPath(timeWindow), { signal: controller.signal })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`Sessions request failed (${response.status})`);
          }
          return response.json() as Promise<Session[]>;
        })
        .then((loadedSessions) => {
          if (controller.signal.aborted) return;
          cache.current = cacheWindowSessions(cache.current, timeWindow, loadedSessions);
          setLatest({ timeWindow, sessions: loadedSessions });
          setKnownSessions((current) => mergeKnownSessions(current, loadedSessions));
          setSessionsVersion((version) => version + 1);
          setIsLoading(false);
          setLoadFailed(false);
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          // keep showing the last good list when a background refresh fails
          if (!background) {
            setLatest({ timeWindow, sessions: [] });
            setIsLoading(false);
            setLoadFailed(true);
          }
          console.error('Failed to load sessions', error);
        });
    };

    if (cached == null) {
      load(false);
    } else {
      setLatest({ timeWindow, sessions: cached });
      setIsLoading(false);
      setLoadFailed(false);
      load(true);
    }
    const refresh = setInterval(() => load(true), REFRESH_INTERVAL_MS);
    return () => {
      clearInterval(refresh);
      controller.abort();
    };
  }, [retryCount, timeWindow]);

  const retrySessions = () => {
    setIsLoading(true);
    setLoadFailed(false);
    setRetryCount((key) => key + 1);
  };

  const sessions =
    latest?.timeWindow === timeWindow ? latest.sessions : (cache.current.get(timeWindow) ?? []);

  return {
    sessions,
    knownSessions,
    isLoading,
    loadFailed,
    sessionsVersion,
    retrySessions,
  };
}
