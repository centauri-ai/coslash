import { useEffect, useRef, useState } from 'react';
import { ApiAuthenticationError, apiFetch, readApiError } from '@/pages/coslash/lib/api';
import { decodeMachineFacts, type MachineFact } from '@/pages/coslash/lib/machines';
import { waitForRemoteRefresh } from '@/pages/coslash/lib/remote-api';
import { isLocalSource, type Session, type SessionIdentity } from '@/pages/coslash/lib/session';
import { exactReadErrorKind, type ExactReadErrorKind } from '@/pages/coslash/lib/session-detail';
import { rangeStart, type SessionRange } from '@/pages/coslash/lib/session-range';
import { MINUTE } from '@/pages/coslash/lib/time';

// Background refresh keeps statuses and "ago" times current.
const REFRESH_INTERVAL_MS = MINUTE;
const ACTIVE_REVIEW_REFRESH_INTERVAL_MS = 3_000;

export function remoteRefreshInProgress(machines: MachineFact[]) {
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

export type FileDiffErrorKind = ExactReadErrorKind | 'authentication';

export type ExactReadFailure = {
  kind: ExactReadErrorKind;
  message: string;
};

const DIFF_FAILURE_MESSAGES: Record<Exclude<ExactReadErrorKind, 'other'>, string> = {
  stale: 'This session changed before its file changes loaded. Refresh the snapshot and try again.',
  missing: 'These exact file changes are no longer available. Refresh the snapshot and try again.',
  corrupt: 'Cached file changes could not be read. Refresh the source to restore a last-good copy.',
  too_large: 'This file’s recorded changes are too large to display.',
};

export function exactDiffFailure(status: number, code: string | undefined): ExactReadFailure {
  const kind = exactReadErrorKind(code);
  return {
    kind,
    message:
      kind === 'other'
        ? `Could not load this exact session revision’s file changes (${status}).`
        : DIFF_FAILURE_MESSAGES[kind],
  };
}

export type SessionsPayload = {
  sessions: Session[];
  machines: MachineFact[];
};

export type SessionsQuery = {
  range: SessionRange;
};

export function decodeSessionsResponse(body: unknown): SessionsPayload {
  if (body == null || typeof body !== 'object') {
    throw new Error('Invalid sessions response');
  }
  const sessions = (body as { sessions?: unknown }).sessions;
  if (!Array.isArray(sessions)) {
    throw new Error('Invalid sessions response');
  }
  return {
    sessions: sessions.map(decodeSession),
    machines: machinesFromBody(body),
  };
}

export async function decodeSessionsResponseWithCostVersion(response: Response, signal: AbortSignal) {
  const body: unknown = await response.json();
  signal.throwIfAborted();
  return {
    ...decodeSessionsResponse(body),
    synthesisCostVersion: response.headers.get('X-Coslash-Synthesis-Cost-Version') || null,
  };
}

// The collector sends every Session field and encodes empty collections as
// [] or {} (sessionWithJSONCollections in collector/cmd/coslash).
export function decodeSession(value: unknown): Session {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid session response');
  }
  return value as Session;
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
  revision = session.detailRevision,
) {
  return `/api/session-detail?${new URLSearchParams({
    source: session.sourceId,
    agent: session.agent,
    session: session.id,
    revision,
  })}`;
}

export function synthesisRequestPath(session: SessionIdentity) {
  if (!isLocalSource(session.sourceId)) {
    throw new Error('remote synthesis unsupported');
  }
  return `/api/synthesis?${new URLSearchParams({ agent: session.agent, id: session.id })}`;
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

export function useFileDiff(selection: FileSelection | null, retryToken: number) {
  const [loaded, setLoaded] = useState<
    | (FileSelection & {
        retryToken: number;
        changes: FileChange[] | null;
        loadError: string | null;
        loadErrorKind: FileDiffErrorKind | null;
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
          throw exactDiffFailure(response.status, (await readApiError(response))?.code);
        }
        const { changes } = (await response.json()) as { changes: FileChange[] };
        if (!controller.signal.aborted) {
          setLoaded({ ...selection, retryToken, changes, loadError: null, loadErrorKind: null });
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted) {
          const failure = error as Partial<ExactReadFailure>;
          setLoaded({
            ...selection,
            retryToken,
            changes: null,
            loadError:
              error instanceof ApiAuthenticationError
                ? error.message
                : (failure.message ?? 'Could not load this exact session revision’s file changes.'),
            loadErrorKind:
              error instanceof ApiAuthenticationError ? 'authentication' : (failure.kind ?? 'other'),
          });
        }
      }
    };
    void load();

    return () => controller.abort();
  }, [retryToken, selection]);

  const isCurrent =
    selection != null &&
    loaded != null &&
    loaded.retryToken === retryToken &&
    sameFileSelection(loaded, selection);
  return {
    changes: isCurrent ? loaded.changes : null,
    isLoading: selection != null && !isCurrent,
    loadError: isCurrent ? loaded.loadError : null,
    loadErrorKind: isCurrent ? loaded.loadErrorKind : null,
  };
}

export function useSessions({ range }: SessionsQuery) {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [machines, setMachines] = useState<MachineFact[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [retryCount, setRetryCount] = useState(0);
  const [refreshCount, setRefreshCount] = useState(0);
  const foregroundKey = useRef<string | null>(null);
  const [sessionsVersion, setSessionsVersion] = useState(0);
  const [synthesisCostVersion, setSynthesisCostVersion] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let authenticationFailed = false;
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    // A programmatic refresh reloads in the background, so a failure keeps the
    // last good list. A new range or an explicit retry is a foreground load.
    const key = `${range}|${retryCount}`;

    const scheduleRefresh = (delay = REFRESH_INTERVAL_MS) => {
      refreshTimer = setTimeout(() => load(true), delay);
    };

    const load = (background: boolean) => {
      if (authenticationFailed) return;
      if (!background) {
        foregroundKey.current = null;
        setIsLoading(true);
        setLoadError(null);
      }
      const since = rangeStart(range);
      const path = sessionsRequestPath({ localSince: since, remoteSince: since });
      apiFetch(path, { signal: controller.signal })
        .then((response) => {
          if (!response.ok) {
            throw new Error(`Sessions request failed (${response.status})`);
          }
          return decodeSessionsResponseWithCostVersion(response, controller.signal);
        })
        .then((payload) => {
          if (controller.signal.aborted) return;
          foregroundKey.current = key;
          setSessions(payload.sessions);
          setMachines(payload.machines);
          setSynthesisCostVersion(payload.synthesisCostVersion);
          setSessionsVersion((version) => version + 1);
          setIsLoading(false);
          setLoadError(null);
          scheduleRefresh(
            payload.sessions.some(({ reviewPending }) => reviewPending)
              ? ACTIVE_REVIEW_REFRESH_INTERVAL_MS
              : REFRESH_INTERVAL_MS,
          );
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
            setSynthesisCostVersion(null);
            setIsLoading(false);
            setLoadError(
              requestAuthenticationFailed ? error.message : 'CoSlash couldn’t load sessions from the API.',
            );
          }
          console.error('Failed to load sessions', error);
          scheduleRefresh();
        });
    };

    load(key === foregroundKey.current);
    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      controller.abort();
    };
  }, [range, retryCount, refreshCount]);

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
        setRefreshCount((count) => count + 1);
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

  const refreshSessions = () => setRefreshCount((count) => count + 1);

  return {
    sessions,
    machines,
    isLoading,
    loadError,
    sessionsVersion,
    synthesisCostVersion,
    retrySessions,
    refreshSessions,
  };
}
