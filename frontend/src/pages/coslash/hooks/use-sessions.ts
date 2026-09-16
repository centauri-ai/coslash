import { useEffect, useRef, useState } from 'react';
import { ApiAuthenticationError, apiFetch } from '@/pages/coslash/lib/api';
import { decodeMachineFacts, type MachineFact } from '@/pages/coslash/lib/machines';
import { remoteRefreshInProgress, waitForRemoteRefresh } from '@/pages/coslash/lib/remote-api';
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

export class PublicationReloadTracker {
  private loaded?: string;
  private latest?: string;
  private requested?: string;
  private inFlight = false;
  private pending = false;
  private observation = 0;
  private requestObservation = 0;

  beginRequest() {
    if (this.inFlight) {
      this.pending = true;
      return false;
    }
    this.inFlight = true;
    this.pending = false;
    this.requestObservation = this.observation;
    return true;
  }

  observe(publicationId: string | undefined) {
    if (publicationId !== this.latest) this.observation += 1;
    this.latest = publicationId;
    if (publicationId == null || publicationId === this.loaded || publicationId === this.requested) {
      return false;
    }
    this.requested = publicationId;
    if (this.inFlight) return false;
    return true;
  }

  accept(publicationId: string | undefined) {
    this.loaded = publicationId;
    // If no status publication arrived after this request began, the sessions
    // response is the newest authority we have. This matters because opaque
    // publication IDs cannot be ordered: a response containing C supersedes
    // the B that triggered it. A status observed during the request still wins
    // until a subsequent sessions response accepts that generation.
    if (this.observation <= this.requestObservation) {
      this.latest = publicationId;
      this.requested = publicationId;
    }
    this.inFlight = false;
    if (this.latest != null && this.latest !== this.loaded) this.pending = true;
    const reload = this.pending;
    this.pending = false;
    return reload;
  }

  fail() {
    this.inFlight = false;
    this.pending = false;
  }

  allowRetry() {
    this.requested = undefined;
  }

  hasPendingPublication() {
    return this.latest != null && this.latest !== this.loaded;
  }
}

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
function decodeSession(value: unknown): Session {
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
  const remoteStatusHandler = useRef<(machine: MachineFact) => boolean>(() => false);
  const backgroundRefreshHandler = useRef<() => void>(() => {});

  useEffect(() => {
    const controller = new AbortController();
    let authenticationFailed = false;
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    let pollingRemote = false;
    const publicationReload = new PublicationReloadTracker();

    const scheduleRefresh = () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      refreshTimer = setTimeout(() => {
        publicationReload.allowRetry();
        load(true);
      }, REFRESH_INTERVAL_MS);
    };

    function load(background: boolean) {
      if (authenticationFailed) return;
      if (!publicationReload.beginRequest()) return;
      if (refreshTimer) clearTimeout(refreshTimer);
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
          const remoteMachine = payload.machines.find((machine) => !isLocalSource(machine.sourceId));
          setSessions(payload.sessions);
          setMachines(payload.machines);
          setSessionsVersion((version) => version + 1);
          setIsLoading(false);
          setLoadError(null);
          const reload = publicationReload.accept(remoteMachine?.publicationId);
          if (remoteMachine != null) startRemotePoll(remoteMachine);
          if (reload) load(true);
          else scheduleRefresh();
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
          publicationReload.fail();
          scheduleRefresh();
        });
    }

    function requestPublication(publicationId: string | undefined) {
      if (publicationReload.observe(publicationId)) load(true);
      return publicationId != null && publicationReload.hasPendingPublication();
    }

    function acceptRemoteStatus(machine: MachineFact) {
      setMachines((current) => current.map((item) => (item.sourceId === machine.sourceId ? machine : item)));
      return requestPublication(machine.publicationId);
    }

    function startRemotePoll(machine: MachineFact) {
      if (pollingRemote || !remoteRefreshInProgress(machine)) return;
      pollingRemote = true;
      void waitForRemoteRefresh(machine, controller.signal, acceptRemoteStatus)
        .then(() => {
          if (!controller.signal.aborted && !publicationReload.hasPendingPublication()) load(true);
        })
        .catch(() => {})
        .finally(() => {
          pollingRemote = false;
        });
    }

    remoteStatusHandler.current = acceptRemoteStatus;
    backgroundRefreshHandler.current = () => load(true);
    load(false);
    return () => {
      if (refreshTimer) clearTimeout(refreshTimer);
      remoteStatusHandler.current = () => false;
      backgroundRefreshHandler.current = () => {};
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
    acceptRemoteStatus: (machine: MachineFact) => remoteStatusHandler.current(machine),
    refreshSessions: () => backgroundRefreshHandler.current(),
  };
}
