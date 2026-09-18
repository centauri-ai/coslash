import type { ShareDestination } from '@/pages/coslash/features/sharing/model';
import { isEligibleForSharing, type Session } from '@/pages/coslash/lib/session';

export const FULL_SESSION_PREVIEW_VERSION = 'full-session-preview/v1' as const;
export const FULL_SESSION_SHARE_VERSION = 'full-session-share/v1' as const;

export type FullSessionSelection = {
  sourceId: string;
  agent: string;
  sessionId: string;
  revisionId: string;
};

export type FullSessionRepository = { canonical: string; localOnly: boolean };

export type FullSessionEnvelope = {
  schemaVersion: 'session-revision/v2';
  mediaType: 'application/vnd.coslash.session-revision.v2+json';
  recordByteCount: number;
  recordSha256: string;
  repository: FullSessionRepository;
  record: Record<string, unknown>;
};

export type FullSessionPreview = {
  adapterVersion: typeof FULL_SESSION_PREVIEW_VERSION;
  state: 'ready' | 'invalid' | 'stale_source' | 'oversized' | 'incompatible_server' | 'unavailable';
  approvalAllowed: boolean;
  selection: FullSessionSelection;
  schemaVersion?: 'session-revision/v2';
  mediaType?: 'application/vnd.coslash.session-revision.v2+json';
  recordBytes?: number;
  payloadBytes?: number;
  maxRecordBytes?: number;
  recordSha256?: string;
  embeddedSecretRisk: boolean;
  envelope?: FullSessionEnvelope;
  problem?: { code: string; message: string; action: string };
};

export type FullSessionConsent = {
  previewContractVersion: typeof FULL_SESSION_PREVIEW_VERSION;
  revisionId: string;
  recordSha256: string;
  recordBytes: number;
  payloadBytes: number;
  repository: FullSessionRepository;
  destinationWorkspaceId: string;
  destinationName: string;
  audienceMemberCount: number;
};

export type FullSessionShareRequest = {
  contractVersion: typeof FULL_SESSION_SHARE_VERSION;
  selection: FullSessionSelection;
  idempotencyKey: string;
  consent: FullSessionConsent;
};

export type FullSessionShareError =
  | 'invalid_full_session_request'
  | 'full_session_invalid'
  | 'full_session_too_large'
  | 'incompatible_server'
  | 'review_binding_changed'
  | 'idempotency_conflict'
  | 'unauthorized'
  | 'rate_limited'
  | 'network_unavailable'
  | 'timeout'
  | 'temporary_unavailable';

export type FullSessionShareResult = {
  contractVersion: typeof FULL_SESSION_SHARE_VERSION;
  state: 'accepted' | 'already_accepted' | 'failed';
  selection: FullSessionSelection;
  idempotencyKey: string;
  repositoryId?: string;
  byteCount?: number;
  contentSha256?: string;
  deduplicated: boolean;
  sharedAt?: string;
  route?: { hubContractVersion: 'full-session-read/v1'; path: string };
  error?: { code: FullSessionShareError; retryable: boolean };
};

export function fullSessionCandidates(sessions: readonly Session[]): Session[] {
  return sessions.filter(
    (session) =>
      session.sourceId !== 'local' &&
      session.agent === 'codex' &&
      typeof session.fullRevision === 'string' &&
      session.fullRevision.length > 0 &&
      isEligibleForSharing(session),
  );
}

export function fullSessionSelection(session: Session): FullSessionSelection {
  if (!session.fullRevision) throw new Error('This session has no exact full revision.');
  return {
    sourceId: session.sourceId,
    agent: session.agent,
    sessionId: session.id,
    revisionId: session.fullRevision,
  };
}

export function bindFullSessionReview(
  session: Session,
  preview: FullSessionPreview,
  destination: ShareDestination,
  idempotencyKey: string,
): FullSessionShareRequest {
  const selection = fullSessionSelection(session);
  if (
    preview.state !== 'ready' ||
    !preview.approvalAllowed ||
    preview.envelope == null ||
    preview.recordSha256 == null ||
    preview.recordBytes == null ||
    preview.payloadBytes == null ||
    JSON.stringify(preview.selection) !== JSON.stringify(selection) ||
    preview.recordSha256 !== preview.envelope.recordSha256 ||
    preview.recordBytes !== preview.envelope.recordByteCount
  ) {
    throw new Error('The full-session preview is not approvable.');
  }
  if (destination.credentialState !== 'paired') {
    throw new Error('The destination credential is not ready.');
  }
  if (idempotencyKey.length < 16 || idempotencyKey.length > 200) {
    throw new Error('The idempotency key is outside the contract bounds.');
  }
  return {
    contractVersion: FULL_SESSION_SHARE_VERSION,
    selection,
    idempotencyKey,
    consent: {
      previewContractVersion: FULL_SESSION_PREVIEW_VERSION,
      revisionId: selection.revisionId,
      recordSha256: preview.recordSha256,
      recordBytes: preview.recordBytes,
      payloadBytes: preview.payloadBytes,
      repository: preview.envelope.repository,
      destinationWorkspaceId: destination.workspaceId,
      destinationName: destination.workspaceName,
      audienceMemberCount: destination.currentMemberCount,
    },
  };
}

export function fullSessionReviewStillCurrent(
  request: FullSessionShareRequest,
  session: Session | undefined,
  preview: FullSessionPreview,
  destination: ShareDestination,
): boolean {
  if (!session?.fullRevision || preview.state !== 'ready') return false;
  return (
    request.selection.sourceId === session.sourceId &&
    request.selection.agent === session.agent &&
    request.selection.sessionId === session.id &&
    request.selection.revisionId === session.fullRevision &&
    request.selection.revisionId === preview.selection.revisionId &&
    request.consent.recordSha256 === preview.recordSha256 &&
    request.consent.recordBytes === preview.recordBytes &&
    request.consent.payloadBytes === preview.payloadBytes &&
    request.consent.destinationWorkspaceId === destination.workspaceId &&
    request.consent.destinationName === destination.workspaceName &&
    request.consent.audienceMemberCount === destination.currentMemberCount &&
    destination.credentialState === 'paired'
  );
}

export function fullSessionResultNeedsReview(result: FullSessionShareResult): boolean {
  return result.state === 'failed' && result.error?.code === 'review_binding_changed';
}
