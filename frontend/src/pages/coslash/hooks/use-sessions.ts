import { useEffect, useState } from 'react';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import { decodeMachineFacts, type MachineFact } from '@/pages/coslash/lib/machines';
import {
  isLocalSource,
  withLocalSourceDefaults,
  type Session,
  type SessionIdentity,
} from '@/pages/coslash/lib/session';
import { MINUTE } from '@/pages/coslash/lib/time';
import { timeWindowStart, type TimeWindow } from '@/pages/coslash/lib/time-window';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;

export type FileSelection = {
  sourceId: string;
  agent: string;
  sessionId: string;
  path: string;
};

export type FileChange = {
  kind: 'diff' | 'content';
  text: string;
  operation: string;
  additions: number;
  deletions: number;
};

export type SessionsPayload = {
  sessions: Session[];
  machines: MachineFact[];
};

export type SessionsQuery = {
  localWindow: TimeWindow;
  remoteWindow: TimeWindow;
};

export function decodeSessionsResponse(body: unknown): SessionsPayload {
  if (Array.isArray(body)) {
    return {
      sessions: body.map((session) => withLocalSourceDefaults(session as Session)),
      machines: [],
    };
  }
  if (body == null || typeof body !== 'object') {
    throw new Error('Invalid sessions response');
  }
  const sessions = (body as { sessions?: unknown }).sessions;
  if (!Array.isArray(sessions)) {
    throw new Error('Invalid sessions response');
  }
  const machinesRaw = (body as { machines?: unknown }).machines;
  return {
    sessions: sessions.map((session) => withLocalSourceDefaults(session as Session)),
    machines: machinesRaw === undefined ? [] : decodeMachineFacts(machinesRaw),
  };
}

/** Independent local/remote cutoffs; omit local `since` for Hub all-history without widening remote. */
export function sessionsRequestPath(query: {
  localSince: number | null;
  remoteSince: number | null;
}): string {
  const params = new URLSearchParams();
  params.set('sourceAware', '1');
  if (query.localSince != null) params.set('since', String(query.localSince));
  if (query.remoteSince != null) params.set('remoteSince', String(query.remoteSince));
  const encoded = params.toString();
  return encoded === '' ? '/api/sessions' : `/api/sessions?${encoded}`;
}
export function diffRequestPath(selection: FileSelection) {
  if (!isLocalSource(selection.sourceId)) {
    throw new Error('remote diff unsupported');
  }
  return `/api/diff?${new URLSearchParams({ id: selection.sessionId, path: selection.path })}`;
}

export function synthesisRequestPath(session: SessionIdentity) {
  if (!isLocalSource(session.sourceId)) {
    throw new Error('remote synthesis unsupported');
  }
  return `/api/synthesis?${new URLSearchParams({ id: session.id })}`;
}

function sameFileSelection(
  left: Pick<FileSelection, 'sourceId' | 'sessionId' | 'path'>,
  right: Pick<FileSelection, 'sourceId' | 'sessionId' | 'path'>,
): boolean {
  return left.sourceId === right.sourceId && left.sessionId === right.sessionId && left.path === right.path;
}

export function useFileDiff(selection: FileSelection | null) {
  const [loaded, setLoaded] = useState<
    (FileSelection & { changes: FileChange[] | null; loadError: string | null }) | null
  >(null);

  useEffect(() => {
    if (selection == null) return;
    if (!isLocalSource(selection.sourceId)) {
      setLoaded({
        ...selection,
        changes: null,
        loadError: 'Remote file diffs are not available.',
      });
      return;
    }
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

  const isCurrent = selection != null && loaded != null && sameFileSelection(loaded, selection);
  return {
    changes: isCurrent ? loaded.changes : null,
    isLoading: selection != null && !isCurrent,
    loadError: isCurrent ? loaded.loadError : null,
  };
}

export function useSessions({ localWindow, remoteWindow }: SessionsQuery) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [machines, setMachines] = useState<MachineFact[]>([]);
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
      const path = sessionsRequestPath({
        localSince: timeWindowStart(localWindow),
        remoteSince: timeWindowStart(remoteWindow),
      });
      apiFetch(path, { signal: controller.signal })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`Sessions request failed (${response.status})`);
          }
          return response.json() as Promise<unknown>;
        })
        .then((body) => {
          if (controller.signal.aborted) return;
          const payload = decodeSessionsResponse(body);
          setSessions(payload.sessions);
          setMachines(payload.machines);
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
            setMachines([]);
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
  }, [localWindow, remoteWindow, retryCount]);

  const retrySessions = () => {
    setIsLoading(true);
    setLoadError(null);
    setRetryCount((key) => key + 1);
  };

  return {
    sessions,
    machines,
    isLoading,
    loadError,
    sessionsVersion,
    retrySessions,
  };
}
