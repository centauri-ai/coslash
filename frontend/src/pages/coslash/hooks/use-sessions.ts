import { useEffect, useState } from 'react';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import type { Session } from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;

export type FileSelection = { sessionId: string; path: string };
export type FileChange = {
  kind: 'diff' | 'content';
  text: string;
  operation: string;
  additions: number;
  deletions: number;
};

export function decodeSessionsResponse(body: unknown): Session[] {
  if (Array.isArray(body)) {
    return body as Session[];
  }
  if (
    body != null &&
    typeof body === 'object' &&
    Array.isArray((body as { sessions?: unknown }).sessions)
  ) {
    return (body as { sessions: Session[] }).sessions;
  }
  throw new Error('Invalid sessions response');
}

export function diffRequestPath(selection: FileSelection) {
  return `/api/diff?${new URLSearchParams({ id: selection.sessionId, path: selection.path })}`;
}

export function useFileDiff(selection: FileSelection | null) {
  const [loaded, setLoaded] = useState<
    (FileSelection & { changes: FileChange[] | null; loadError: string | null }) | null
  >(null);

  useEffect(() => {
    if (selection == null) return;
    const controller = new AbortController();

    apiFetch(diffRequestPath(selection), { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Diff request failed (${response.status})`);
        return response.json() as Promise<{ changes: FileChange[] }>;
      })
      .then(({ changes }) => {
        if (!controller.signal.aborted) setLoaded({ ...selection, changes, loadError: null });
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setLoaded({
            ...selection,
            changes: null,
            loadError: 'Could not load this session’s file changes.',
          });
        }
      });

    return () => controller.abort();
  }, [selection]);

  const isCurrent =
    selection != null && loaded?.sessionId === selection.sessionId && loaded.path === selection.path;
  return {
    changes: isCurrent ? loaded.changes : null,
    isLoading: selection != null && !isCurrent,
    loadError: isCurrent ? loaded.loadError : null,
  };
}

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
      const path = since == null
        ? '/api/sessions'
        : `/api/sessions?since=${since}&remoteSince=${since}`;
      apiFetch(path, { signal: controller.signal })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`Sessions request failed (${response.status})`);
          }
          return response.json() as Promise<unknown>;
        })
        .then((body) => {
          if (controller.signal.aborted) return;
          setSessions(decodeSessionsResponse(body));
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
