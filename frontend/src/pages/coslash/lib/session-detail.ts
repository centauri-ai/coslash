import type { FileSelection } from '@/pages/coslash/hooks/use-sessions';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  isLocalSession,
  sessionKey,
  type Session,
  type SessionDetail,
  type SessionIdentity,
} from '@/pages/coslash/lib/session';

export type SynthesisResponse = {
  synthesis: SessionDetail['synthesis'];
  synthesisPending: boolean;
  synthesisError?: string;
  revision: number;
};

export type DetailResponse = {
  sourceId: string;
  agent: string;
  sessionId: string;
  revision: string;
  synthesisRevision?: number;
  cachedOffline: boolean;
  session: Partial<Session>;
};

export type ExactReadErrorKind = 'stale' | 'missing' | 'corrupt' | 'too_large' | 'other';

export type DetailErrorKind = Exclude<ExactReadErrorKind, 'too_large'> | 'authentication';

export type DetailError = {
  key: string;
  retryToken: number;
  kind: DetailErrorKind;
  message: string;
};

export type DetailFailure = Omit<DetailError, 'key' | 'retryToken'>;

export function needsSourceRefresh(kind: DetailErrorKind): boolean {
  return kind === 'stale' || kind === 'missing' || kind === 'corrupt';
}

/** The failure kind of an exact detail or diff read, from the API error code. */
export function exactReadErrorKind(code: string | undefined): ExactReadErrorKind {
  switch (code) {
    case 'session_detail_stale':
      return 'stale';
    case 'session_detail_missing':
    case 'session_change_missing':
      return 'missing';
    case 'session_detail_corrupt':
      return 'corrupt';
    case 'session_diff_too_large':
      return 'too_large';
    default:
      return 'other';
  }
}

const DETAIL_FAILURE_MESSAGES = {
  stale: 'This session changed before its details loaded. Refresh sessions to inspect the latest revision.',
  missing: 'Complete details for this session are unavailable.',
  corrupt: 'Cached session details could not be read. Refresh the source to restore a last-good copy.',
};

export function detailFailure(status: number, code: string | undefined): DetailFailure {
  const kind = exactReadErrorKind(code);
  return kind === 'too_large' || kind === 'other'
    ? { kind: 'other', message: `Could not load session details (${status}).` }
    : { kind, message: DETAIL_FAILURE_MESSAGES[kind] };
}

export function filePanelOpen(
  selection: FileSelection | null,
  session: (SessionIdentity & Pick<Session, 'detailRevision'>) | null,
): boolean {
  return (
    selection != null &&
    session != null &&
    selection.sourceId === session.sourceId &&
    selection.agent === session.agent &&
    selection.sessionId === session.id &&
    selection.revision === session.detailRevision
  );
}

export function detailRequestKey(session: Session): string {
  const revision = isLocalSession(session) && session.agent === 'pi' ? session.detailRevision : 'full';
  return `${sessionKey(session)}@${session.detailRevision === '' ? 'summary' : revision}`;
}

export function synthesisAttemptKey(session: Session): string {
  return `${detailRequestKey(session)}@${session.detailRevision}`;
}

export function synthesisMatchesSnapshot(
  result: Pick<SynthesisResponse, 'revision'>,
  synthesisRevision: number,
): boolean {
  return synthesisRevision > 0 && result.revision === synthesisRevision;
}

export function synthesisSettlement(
  pendingKey: string | null,
  key: string,
  result: Pick<SynthesisResponse, 'revision' | 'synthesisPending'>,
  synthesisRevision: number,
) {
  return {
    pendingKey: result.synthesisPending ? key : null,
    settled: !result.synthesisPending && pendingKey === key,
    matchesSnapshot: synthesisMatchesSnapshot(result, synthesisRevision),
  };
}

export function refreshSourceAndRetry(
  refresh: () => void | Promise<void>,
  retry: () => void,
  isCurrent: () => boolean,
): Promise<void> {
  return Promise.resolve(refresh()).then(() => {
    if (isCurrent()) retry();
  });
}

export function detailAttemptState(
  loaded: { key: string; retryToken: number } | null,
  error: DetailError | null,
  key: string,
  retryToken: number,
) {
  const currentError = error?.key === key && error.retryToken === retryToken ? error : null;
  const hasSnapshot = loaded?.key === key && currentError?.kind !== 'authentication';
  return {
    hasSnapshot,
    isLoading: currentError == null && (!hasSnapshot || loaded.retryToken !== retryToken),
    error: currentError,
  };
}

export function snapshotMayBeStale(detail: SessionDetail, current: Session): boolean {
  return current.status != null || detail.detailRevision !== current.detailRevision;
}

export function detailPresentation(session: Session | null): {
  detail: SessionDetail | null;
  summaryOnly: boolean;
} {
  if (session == null) return { detail: null, summaryOnly: false };
  return session.detailRevision === ''
    ? { detail: session, summaryOnly: true }
    : { detail: null, summaryOnly: false };
}

export function cachedOfflineWarning(
  cachedOffline: boolean,
  state: MachineFact['state'] | undefined,
): boolean {
  return state == null ? cachedOffline : state !== 'ok' && state !== 'limited';
}

export function overlayLiveSessionFields(detail: SessionDetail, current: Session): SessionDetail {
  if (detail.detailRevision !== current.detailRevision && current.mtime <= detail.mtime) return detail;
  const currentSubagents = new Map(current.subagents.map((subagent) => [subagent.id, subagent]));
  return {
    ...detail,
    status: current.status,
    ...(current.agent === 'pi' ? { entrypoint: current.entrypoint } : {}),
    branch: current.branch,
    repo: current.repo,
    repoLocalOnly: current.repoLocalOnly,
    locations: isLocalSession(current) ? current.locations : (detail.locations ?? current.locations),
    reviewPending: current.reviewPending,
    reviewError: current.reviewError,
    completion: current.completion,
    privacy: current.privacy,
    shareEligibility: current.shareEligibility,
    eligibleForAggregates: current.eligibleForAggregates,
    displayStale: current.displayStale,
    lastSeenStatus: current.lastSeenStatus,
    launchable: current.launchable,
    launchBlockReason: current.launchBlockReason,
    ...(isLocalSession(current)
      ? { mtime: current.mtime, commits: current.commits, git: current.git, lastEditAt: current.lastEditAt }
      : {}),
    subagents: detail.subagents.map((subagent) => {
      const live = currentSubagents.get(subagent.id);
      return live == null ? subagent : { ...subagent, status: live.status };
    }),
  };
}
