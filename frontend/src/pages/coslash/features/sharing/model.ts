import { isEligibleForSharing, isLocalSession, type Session } from '@/pages/coslash/lib/session';

// C4 contract; provider fixtures live in coslash-server/testdata/hub-share-v1.
export const HUB_SHARE_VERSION = 'hub-share/v1' as const;
export const MAX_SHARE_ITEMS = 100;

export type EligibilityState =
  'signed_out' | 'pairing_required' | 'credential_dormant' | 'credential_revoked' | 'ready';

export type ShareDestination = {
  workspaceId: string;
  workspaceName: string;
  currentMemberCount: number;
  resultingMemberCount: number;
  currentApprovedSessionCount: number;
  historyDisclosure: string;
  credentialState: 'paired' | 'dormant' | 'revoked';
  audienceVersion: string;
};

type DestinationBase = {
  contractVersion: typeof HUB_SHARE_VERSION;
  configured: boolean;
  hubUrl?: string;
};

export type DestinationResult = DestinationBase &
  (
    | { state: 'ready'; destination: ShareDestination }
    | { state: Exclude<EligibilityState, 'ready'>; destination?: never }
  );

export type ConsentBinding = {
  previewContractVersion: 'backup-preview/v1';
  bundleId: string;
  sourceRevision: string;
  selectedRevision: number;
  completeBackupSha256: string;
  totalBytes: number;
  destinationWorkspaceId: string;
  destinationName: string;
  audienceMemberCount: number;
  audienceVersion: string;
  serverId: string;
  maxBackupBytes: number;
  maxBackupChunkBytes: number;
  backupWorkspaceBytes: number;
};

export type BackupSelection = {
  sourceKind: 'local' | 'ssh';
  sourceId: string;
  agent: string;
  sessionId: string;
};

export type BackupCoverage = {
  artifactCount: number;
  artifactCounts: { kind: string; count: number }[];
  totalBytes: number;
  revisionSha256: string;
  problems: { code: string; memberId: string; kind: string; retryable: boolean }[];
};

export type BackupCapability = {
  serverId: string;
  maxBackupBytes: number;
  maxBackupChunkBytes: number;
  backupWorkspaceBytes: number;
  backupUploadExpiresSeconds: number;
};

export type BackupPreview = {
  adapterVersion: 'backup-preview/v1';
  state: 'ready' | 'blocked' | 'invalid' | 'unavailable' | 'incompatible_server' | 'capacity_rejected';
  approvalAllowed: boolean;
  selection: BackupSelection;
  bundleId?: string;
  sourceRevision?: string;
  coverage: BackupCoverage;
  capability?: BackupCapability;
  audienceVersion?: string;
  problem?: { code: string; message: string; action: string; retryable: boolean };
};

export type ShareItemRequest = {
  localSessionId: string;
  selection: BackupSelection;
  idempotencyKey: string;
  consent: ConsentBinding;
};

export type ShareRequest = {
  contractVersion: typeof HUB_SHARE_VERSION;
  requestId: string;
  items: ShareItemRequest[];
};

export type ShareError =
  | 'invalid_share_request'
  | 'complete_backup_unsupported'
  | 'incompatible_server'
  | 'backup_manifest_invalid'
  | 'stale_backup_review'
  | 'backup_chunk_invalid'
  | 'backup_incomplete'
  | 'backup_capacity_exceeded'
  | 'backup_upload_expired'
  | 'backup_upload_aborted'
  | 'not_found'
  | 'forbidden'
  | 'source_deleted'
  | 'unauthorized'
  | 'credential_dormant'
  | 'credential_revoked'
  | 'destination_changed'
  | 'idempotency_conflict'
  | 'rate_limited'
  | 'network_unavailable'
  | 'timeout'
  | 'temporary_unavailable'
  | 'share_failed';

export type ItemError = { code: ShareError; retryable: boolean; retryAfterSeconds?: number };

export type RouteHandoff = {
  hubContractVersion: 'session-backup-read/v1';
  repositoryId: string;
  path: string;
};

export type ShareItemResult =
  | {
      localSessionId: string;
      idempotencyKey: string;
      state: 'accepted' | 'already_accepted';
      revisionId: string;
      deduplicated: boolean;
      sharedAt: string;
      route: RouteHandoff;
    }
  | {
      localSessionId: string;
      idempotencyKey: string;
      state: 'failed';
      deduplicated: false;
      error: ItemError;
    };

export type ShareResult = {
  contractVersion: typeof HUB_SHARE_VERSION;
  requestId: string;
  state: 'succeeded' | 'partial' | 'failed';
  results: ShareItemResult[];
};

export type ShareWindow = '7d' | '30d' | 'all';
export type ShareCandidate = { session: Session; previouslyShared: boolean };

export const COMPLETE_BACKUP_SUPPORT_MESSAGE = 'Complete backup supports local and SSH Codex sessions only.';

export const RETRY_RULES: Record<
  ShareError,
  { renewedReview: boolean; refreshDestination?: boolean; action: string }
> = {
  invalid_share_request: {
    renewedReview: true,
    action: 'Review the selected sessions again.',
  },
  complete_backup_unsupported: {
    renewedReview: true,
    action: 'Complete backup currently supports local and SSH Codex sessions only.',
  },
  incompatible_server: {
    renewedReview: true,
    action: 'Update the Hub before sharing a complete backup.',
  },
  backup_manifest_invalid: {
    renewedReview: true,
    action: 'Rebuild and review the complete backup.',
  },
  stale_backup_review: {
    renewedReview: true,
    action: 'Review the current backup, destination, audience, and capacity again.',
  },
  backup_chunk_invalid: {
    renewedReview: true,
    action: 'Rebuild the frozen backup before retrying.',
  },
  backup_incomplete: {
    renewedReview: false,
    action: 'Resume the missing verified chunks with the same upload identity.',
  },
  backup_capacity_exceeded: {
    renewedReview: true,
    action: 'The Hub cannot accept this complete backup at its current capacity.',
  },
  backup_upload_expired: {
    renewedReview: true,
    action: 'Build a fresh upload intent and review it again.',
  },
  backup_upload_aborted: {
    renewedReview: true,
    action: 'Build a fresh upload intent and review it again.',
  },
  not_found: {
    renewedReview: false,
    action: 'Retry with the frozen backup and original idempotency key.',
  },
  forbidden: {
    renewedReview: true,
    refreshDestination: true,
    action: 'Restore workspace access and review the destination again.',
  },
  source_deleted: {
    renewedReview: false,
    action: 'The source was deleted and cannot be shared.',
  },
  unauthorized: {
    renewedReview: false,
    refreshDestination: true,
    action: 'Sign in or pair again, then retry the unchanged selection.',
  },
  credential_dormant: {
    renewedReview: true,
    refreshDestination: true,
    action: 'Select the paired workspace and review the destination again.',
  },
  credential_revoked: {
    renewedReview: true,
    refreshDestination: true,
    action: 'Pair this device again before sharing.',
  },
  destination_changed: {
    renewedReview: true,
    refreshDestination: true,
    action: 'Review the current workspace destination and approve it again.',
  },
  idempotency_conflict: {
    renewedReview: true,
    action: 'Stop retrying this key and build a new preview.',
  },
  rate_limited: {
    renewedReview: false,
    action: 'Keep the selection and retry after the server delay.',
  },
  network_unavailable: {
    renewedReview: false,
    action: 'Keep the selection and retry when the network returns.',
  },
  timeout: {
    renewedReview: false,
    action: 'Check upload status with the same key before retrying.',
  },
  temporary_unavailable: {
    renewedReview: false,
    action: 'Keep failed items selected and retry with their original keys.',
  },
  share_failed: {
    renewedReview: false,
    action: 'Keep failed items selected and retry with their original keys.',
  },
};

export function localSessionId(session: Pick<Session, 'sourceId' | 'agent' | 'id'>): string {
  // The opaque source ID is part of the local orchestration identity only. It
  // lets a local and SSH session with the same vendor ID remain independently
  // reviewable without putting a host, username, or path in the request.
  return `${session.sourceId}:${session.agent}:${session.id}`;
}

/**
 * Compatibility wrapper for the pre-LB-04 uploader. It must not receive an
 * ineligible source while its request key is still local-only. LB-04 consumes
 * eligibleSessionCandidates from lib/session-library instead, where source
 * identity is retained for both local and SSH workspaces.
 */
export function localShareCandidates(candidates: ShareCandidate[]): ShareCandidate[] {
  return candidates.filter(({ session }) => isLocalSession(session) && isEligibleForSharing(session));
}

export function filterShareCandidates(
  candidates: ShareCandidate[],
  search: string,
  window: ShareWindow,
  now = Date.now(),
): ShareCandidate[] {
  const normalized = search.trim().toLowerCase();
  const minimum = window === 'all' ? null : now - (window === '7d' ? 7 : 30) * 24 * 60 * 60 * 1000;
  return candidates.filter(({ session }) => {
    if (minimum != null && session.status == null && session.mtime < minimum) return false;
    if (!normalized) return true;
    return [session.name, session.repo, session.branch, session.id, session.agent]
      .filter((value): value is string => value != null)
      .some((value) => value.toLowerCase().includes(normalized));
  });
}

export function isCompleteBackupCandidate(candidate: ShareCandidate): boolean {
  return candidate.session.agent === 'codex';
}

// Hidden approval is unsafe. Narrowing either filter removes newly hidden rows.
export function reconcileVisibleSelection(
  selected: ReadonlySet<string>,
  visible: ShareCandidate[],
): Set<string> {
  const visibleKeys = new Set(
    visible.filter(isCompleteBackupCandidate).map(({ session }) => localSessionId(session)),
  );
  return new Set([...selected].filter((key) => visibleKeys.has(key)));
}

export function limitShareSelection(
  selected: ReadonlySet<string>,
  candidates: ShareCandidate[],
): Set<string> {
  return new Set([...reconcileVisibleSelection(selected, candidates)].slice(0, MAX_SHARE_ITEMS));
}

export function toggleCandidate(selected: ReadonlySet<string>, key: string): Set<string> {
  const next = new Set(selected);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  return next;
}

export function toggleCandidateGroup(
  selected: ReadonlySet<string>,
  candidates: ShareCandidate[],
): Set<string> {
  const next = new Set(selected);
  const keys = candidates.filter(isCompleteBackupCandidate).map(({ session }) => localSessionId(session));
  const allSelected = keys.length > 0 && keys.every((key) => next.has(key));
  for (const key of keys) {
    if (allSelected) next.delete(key);
    else next.add(key);
  }
  return next;
}

export function backupSelection(session: Pick<Session, 'sourceId' | 'id' | 'agent'>): BackupSelection {
  return {
    sourceKind: isLocalSession(session) ? 'local' : 'ssh',
    sourceId: session.sourceId,
    agent: session.agent,
    sessionId: session.id,
  };
}

export function bindBackupConsent(
  session: Pick<Session, 'sourceId' | 'id' | 'agent' | 'mtime'>,
  preview: BackupPreview,
  destination: ShareDestination,
  idempotencyKey: string,
): ShareItemRequest {
  if (
    preview.adapterVersion !== 'backup-preview/v1' ||
    preview.state !== 'ready' ||
    !preview.approvalAllowed ||
    preview.bundleId == null ||
    preview.sourceRevision == null ||
    preview.coverage.revisionSha256 !== preview.bundleId ||
    preview.capability == null ||
    preview.audienceVersion !== destination.audienceVersion ||
    preview.selection.sourceId !== session.sourceId ||
    preview.selection.agent !== session.agent ||
    preview.selection.sessionId !== session.id
  ) {
    throw new Error('The complete backup preview no longer matches the selected session.');
  }
  if (destination.credentialState !== 'paired') {
    throw new Error('The destination credential is not ready.');
  }
  if (idempotencyKey.length < 16 || idempotencyKey.length > 200) {
    throw new Error('The idempotency key is outside the contract bounds.');
  }
  return {
    localSessionId: localSessionId(session),
    selection: preview.selection,
    idempotencyKey,
    consent: {
      previewContractVersion: preview.adapterVersion,
      bundleId: preview.bundleId,
      sourceRevision: preview.sourceRevision,
      selectedRevision: session.mtime,
      completeBackupSha256: preview.coverage.revisionSha256,
      totalBytes: preview.coverage.totalBytes,
      destinationWorkspaceId: destination.workspaceId,
      destinationName: destination.workspaceName,
      audienceMemberCount: destination.currentMemberCount,
      audienceVersion: destination.audienceVersion,
      serverId: preview.capability.serverId,
      maxBackupBytes: preview.capability.maxBackupBytes,
      maxBackupChunkBytes: preview.capability.maxBackupChunkBytes,
      backupWorkspaceBytes: preview.capability.backupWorkspaceBytes,
    },
  };
}

export function consentStillCurrent(
  item: ShareItemRequest,
  session: Pick<Session, 'sourceId' | 'id' | 'agent' | 'mtime'>,
  preview: BackupPreview,
  destination: ShareDestination,
): boolean {
  return (
    preview.state === 'ready' &&
    preview.approvalAllowed &&
    item.localSessionId === localSessionId(session) &&
    item.consent.previewContractVersion === preview.adapterVersion &&
    item.consent.sourceRevision === preview.sourceRevision &&
    item.consent.selectedRevision === session.mtime &&
    item.consent.bundleId === preview.bundleId &&
    item.consent.completeBackupSha256 === preview.coverage.revisionSha256 &&
    item.consent.totalBytes === preview.coverage.totalBytes &&
    item.consent.destinationWorkspaceId === destination.workspaceId &&
    item.consent.destinationName === destination.workspaceName &&
    item.consent.audienceMemberCount === destination.currentMemberCount &&
    item.consent.audienceVersion === destination.audienceVersion &&
    item.consent.serverId === preview.capability?.serverId &&
    item.consent.maxBackupBytes === preview.capability?.maxBackupBytes &&
    item.consent.maxBackupChunkBytes === preview.capability?.maxBackupChunkBytes &&
    item.consent.backupWorkspaceBytes === preview.capability?.backupWorkspaceBytes &&
    destination.credentialState === 'paired'
  );
}

export function planShareRetry(result: ShareResult): {
  unchanged: Set<string>;
  renewedReview: Set<string>;
  refreshDestination: Set<string>;
} {
  const plan = {
    unchanged: new Set<string>(),
    renewedReview: new Set<string>(),
    refreshDestination: new Set<string>(),
  };
  for (const item of result.results) {
    if (item.state !== 'failed' || !item.error.retryable) continue;
    const rule = RETRY_RULES[item.error.code];
    plan[rule.renewedReview ? 'renewedReview' : 'unchanged'].add(item.localSessionId);
    if (rule.refreshDestination) plan.refreshDestination.add(item.localSessionId);
  }
  return plan;
}

export function primarySuccessRoute(result: ShareResult): RouteHandoff | null {
  const success = result.results.find(
    (item): item is Extract<ShareItemResult, { state: 'accepted' | 'already_accepted' }> =>
      item.state !== 'failed' &&
      item.route.repositoryId.length > 0 &&
      isCanonicalBackupRoute(item.revisionId, item.route.path),
  );
  return success?.route ?? null;
}

export function isCanonicalBackupRoute(revisionId: string, path: string): boolean {
  return revisionId.length > 0 && path === `/v3/session-backups/${encodeURIComponent(revisionId)}`;
}

export function hubRouteURL(hubURL: string, path: string): string {
  if (!/^\/(?!\/)[^?#]+$/.test(path)) {
    throw new Error('The Hub route is outside the expected contract.');
  }
  const base = new URL(`${hubURL.replace(/\/+$/, '')}/`);
  const route = new URL(path.slice(1), base);
  if (route.origin !== base.origin || !route.pathname.startsWith(base.pathname)) {
    throw new Error('The Hub route is outside the expected contract.');
  }
  return route.toString();
}
