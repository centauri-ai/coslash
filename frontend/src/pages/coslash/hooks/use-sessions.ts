import { useEffect, useState } from 'react';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import { decodeMachineFacts, type MachineFact } from '@/pages/coslash/lib/machines';
import { waitForRemoteRefresh } from '@/pages/coslash/lib/remote-api';
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
function remoteRefreshInProgress(machines: MachineFact[]) {
  return machines.some(
    (machine) =>
      !isLocalSource(machine.sourceId) &&
      (machine.refreshing || machine.state === 'connecting' || machine.reason === 'initial_refresh'),
  );
}

export type FileSelection = {
  sourceId: string;
  agent: string;
  sessionId: string;
  revision: string;
  path: string;
  changeIds: string[];
};

export type FileChange = {
  kind: 'diff' | 'content';
  text: string;
  operation: string;
  additions: number;
  deletions: number;
};

export type ExactReadErrorKind = 'stale' | 'missing' | 'corrupt' | 'too_large' | 'other';

export type ExactReadFailure = {
  kind: ExactReadErrorKind;
  message: string;
};

export function exactDiffFailure(status: number, code: string): ExactReadFailure {
  switch (code) {
    case 'session_detail_stale':
      return {
        kind: 'stale',
        message: 'This session changed before its file changes loaded. Refresh sessions and try again.',
      };
    case 'session_detail_missing':
    case 'session_change_missing':
      return {
        kind: 'missing',
        message: 'These exact file changes are no longer available. Refresh sessions and try again.',
      };
    case 'session_detail_corrupt':
      return {
        kind: 'corrupt',
        message: 'Cached file changes could not be read. Refresh the source to restore a last-good copy.',
      };
    case 'session_diff_too_large':
      return { kind: 'too_large', message: 'This file’s recorded changes are too large to display.' };
    default:
      return {
        kind: 'other',
        message: `Could not load this exact session revision’s file changes (${status}).`,
      };
  }
}

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
      sessions: body.map(decodeSession),
      machines: [],
    };
  }
  if (body == null || typeof body !== 'object') {
    throw new Error('Invalid sessions response');
  }
  const sessions = (body as { sessions?: unknown }).sessions;
  if (sessions == null) {
    return {
      sessions: [],
      machines: machinesFromBody(body),
    };
  }
  if (!Array.isArray(sessions)) {
    throw new Error('Invalid sessions response');
  }
  return {
    sessions: sessions.map(decodeSession),
    machines: machinesFromBody(body),
  };
}

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

// Older caches and sparse remote facts can contain null collection fields.
// Normalize at the API boundary so one incomplete session cannot crash the
// whole board while the backend is being upgraded or refreshed.
export function decodeSession(value: unknown): Session {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid session response');
  }
  const raw = value as Session;
  const synthesis =
    raw.synthesis == null
      ? null
      : {
          ...raw.synthesis,
          goals: arrayOrEmpty(raw.synthesis.goals),
          keyDecisions: arrayOrEmpty(raw.synthesis.keyDecisions),
        };
  return {
    ...withLocalSourceDefaults(raw),
    tokens: raw.tokens != null && typeof raw.tokens === 'object' ? raw.tokens : {},
    unpricedModels: arrayOrEmpty(raw.unpricedModels),
    subagents: arrayOrEmpty(raw.subagents),
    commands: arrayOrEmpty(raw.commands),
    commits: arrayOrEmpty(raw.commits),
    todos: arrayOrEmpty(raw.todos),
    digest: arrayOrEmpty(raw.digest),
    fileEdits: arrayOrEmpty(raw.fileEdits),
    synthesis,
  };
}

function machinesFromBody(body: object): MachineFact[] {
  const machinesRaw = (body as { machines?: unknown }).machines;
  return machinesRaw === undefined ? [] : decodeMachineFacts(machinesRaw);
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
  const params = new URLSearchParams({
    source: selection.sourceId,
    agent: selection.agent,
    session: selection.sessionId,
    revision: selection.revision,
  });
  selection.changeIds.forEach((changeId) => params.append('change', changeId));
  return `/api/diff?${params}`;
}

export function sessionDetailRequestPath(
  session: Pick<Session, 'sourceId' | 'agent' | 'id' | 'detailRevision'>,
) {
  return `/api/session-detail?${new URLSearchParams({
    source: session.sourceId,
    agent: session.agent,
    session: session.id,
    revision: session.detailRevision,
  })}`;
}

export function synthesisRequestPath(session: SessionIdentity) {
  if (!isLocalSource(session.sourceId)) {
    throw new Error('remote synthesis unsupported');
  }
  return `/api/synthesis?${new URLSearchParams({ id: session.id })}`;
}

function sameFileSelection(left: FileSelection, right: FileSelection): boolean {
  return (
    left.sourceId === right.sourceId &&
    left.agent === right.agent &&
    left.sessionId === right.sessionId &&
    left.revision === right.revision &&
    left.path === right.path &&
    left.changeIds.join('\0') === right.changeIds.join('\0')
  );
}

export function useFileDiff(selection: FileSelection | null) {
  const [loaded, setLoaded] = useState<
    | (FileSelection & {
        changes: FileChange[] | null;
        loadError: string | null;
        loadErrorKind: ExactReadErrorKind | null;
      })
    | null
  >(null);

  useEffect(() => {
    if (selection == null) return;
    const controller = new AbortController();

    const load = async () => {
      try {
        const response = await apiFetch(diffRequestPath(selection), { signal: controller.signal });
        if (!response.ok) {
          let code = '';
          try {
            code = ((await response.json()) as { code?: string }).code ?? '';
          } catch {
            // Status is sufficient for the generic fallback.
          }
          throw exactDiffFailure(response.status, code);
        }
        const { changes } = (await response.json()) as { changes: FileChange[] };
        if (!controller.signal.aborted) {
          setLoaded({ ...selection, changes, loadError: null, loadErrorKind: null });
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted) {
          const failure = error as Partial<ExactReadFailure>;
          setLoaded({
            ...selection,
            changes: null,
            loadError:
              error instanceof ApiAuthenticationError
                ? error.message
                : (failure.message ?? 'Could not load this exact session revision’s file changes.'),
            loadErrorKind: failure.kind ?? 'other',
          });
        }
      }
    };
    void load();

    return () => controller.abort();
  }, [selection]);

  const isCurrent = selection != null && loaded != null && sameFileSelection(loaded, selection);
  return {
    changes: isCurrent ? loaded.changes : null,
    isLoading: selection != null && !isCurrent,
    loadError: isCurrent ? loaded.loadError : null,
    loadErrorKind: isCurrent ? loaded.loadErrorKind : null,
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
    let authenticationFailed = false;
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;

    const scheduleRefresh = () => {
      refreshTimer = setTimeout(() => load(true), REFRESH_INTERVAL_MS);
    };

    const load = (background: boolean) => {
      if (authenticationFailed) return;
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
          scheduleRefresh();
        })
        .catch((error: unknown) => {
          if (controller.signal.aborted) return;
          const requestAuthenticationFailed = error instanceof ApiAuthenticationError;
          if (requestAuthenticationFailed) authenticationFailed = true;
          // Keep showing the last good list when an ordinary background
          // refresh fails. Authentication failures invalidate that private data.
          if (!background || requestAuthenticationFailed) {
            setSessions([]);
            setMachines([]);
            setIsLoading(false);
            setLoadError(
              requestAuthenticationFailed ? error.message : 'CoSlash couldn’t load sessions from the API.',
            );
          }
          console.error('Failed to load sessions', error);
          scheduleRefresh();
        });
    };

    load(false);
    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      controller.abort();
    };
  }, [localWindow, remoteWindow, retryCount]);

  const remoteRefreshing = remoteRefreshInProgress(machines);

  useEffect(() => {
    if (!remoteRefreshing) return;
    const controller = new AbortController();
    void waitForRemoteRefresh(undefined, controller.signal)
      .then((machine) => {
        if (controller.signal.aborted) return;
        setMachines((current) =>
          current.map((item) => (item.sourceId === machine.sourceId ? machine : item)),
        );
        // Remote collection has reached a final state, so reload its sessions
        // rather than waiting for the normal background refresh.
        setRetryCount((count) => count + 1);
      })
      .catch(() => {});
    return () => {
      controller.abort();
    };
  }, [remoteRefreshing]);

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
