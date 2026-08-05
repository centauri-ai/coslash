import { useEffect, useState } from 'react';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import type { Session } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;

export function useSessions(timeWindow: TimeWindow) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [retryCount, setRetryCount] = useState(0);
  const [sessionsVersion, setSessionsVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    const load = (background: boolean) => {
      if (!background) {
        setIsLoading(true);
        setLoadError(null);
      }
      const since = timeWindowStart(timeWindow);
      const path = since == null ? '/api/sessions' : `/api/sessions?since=${since}`;
      apiFetch(path, { signal: controller.signal })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`Sessions request failed (${response.status})`);
          }
          return response.json() as Promise<Session[]>;
        })
        .then((loadedSessions) => {
          if (controller.signal.aborted) return;
          setSessions(loadedSessions);
          setSessionsVersion((version) => version + 1);
          setIsLoading(false);
          setLoadError(null);
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          const authenticationFailed = error instanceof ApiAuthenticationError;
          // Keep showing the last good list when an ordinary background
          // refresh fails. Authentication failures invalidate that private data.
          if (!background || authenticationFailed) {
            setSessions([]);
            setIsLoading(false);
            setLoadError(
              authenticationFailed ? error.message : 'CoSlash couldn’t load sessions from the API.',
            );
          }
          console.error('Failed to load sessions', error);
        });
    };

    load(false);
    const refresh = setInterval(() => load(true), REFRESH_INTERVAL_MS);
    return () => {
      clearInterval(refresh);
      controller.abort();
    };
  }, [retryCount, timeWindow]);

  const retrySessions = () => {
    setIsLoading(true);
    setLoadError(null);
    setRetryCount((key) => key + 1);
  };

  return {
    sessions,
    isLoading,
    loadError,
    sessionsVersion,
    retrySessions,
  };
}
