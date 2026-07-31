import { useEffect, useState } from 'react';
import type { Session } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;

// Fetches every session; callers filter by time window client-side, so
// changing the window never refetches.
export function useSessions() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [retryCount, setRetryCount] = useState(0);
  const [sessionsVersion, setSessionsVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    const load = (background: boolean) => {
      if (!background) {
        setIsLoading(true);
        setLoadFailed(false);
      }
      fetch('/api/sessions', { signal: controller.signal })
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
          setLoadFailed(false);
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          // keep showing the last good list when a background refresh fails
          if (!background) {
            setSessions([]);
            setIsLoading(false);
            setLoadFailed(true);
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
  }, [retryCount]);

  const retrySessions = () => {
    setIsLoading(true);
    setLoadFailed(false);
    setRetryCount((key) => key + 1);
  };

  return { sessions, isLoading, loadFailed, sessionsVersion, retrySessions };
}
