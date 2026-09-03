import { canonicalUploadBytes, type SnapshotPreview } from '@/pages/coslash/lib/preview';
import { isLocalSession, type Session } from '@/pages/coslash/lib/session';

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
  previewContractVersion: 'snapshot-preview/v1';
  sourceRevision: number;
  contentHash: string;
  payloadBytes: number;
  destinationWorkspaceId: string;
};

export type ShareItemRequest = {
  localSessionId: string;
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
  | 'unsupported_snapshot_version'
  | 'snapshot_invalid'
  | 'snapshot_too_large'
  | 'source_deleted'
  | 'unauthorized'
  | 'credential_dormant'
  | 'credential_revoked'
  | 'consent_stale'
  | 'destination_changed'
  | 'idempotency_conflict'
  | 'rate_limited'
  | 'network_unavailable'
  | 'timeout'
  | 'temporary_unavailable'
  | 'share_failed';

export type ItemError = { code: ShareError; retryable: boolean; retryAfterSeconds?: number };

export type RouteHandoff = {
  hubContractVersion: 'hub-read/v1';
  repositoryId: string;
  canonicalWeekStart: string;
  path: string;
};

export type ShareItemResult =
  | {
      localSessionId: string;
      idempotencyKey: string;
      state: 'accepted' | 'already_accepted';
      sessionId: string;
      revisionId: string;
      deduplicated: boolean;
      sharedAt: string;
      briefState: 'pending' | 'ready' | 'failed' | 'unavailable';
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
export type ShareCandidate = { session: Session };

export const RETRY_RULES: Record<
  ShareError,
  { renewedReview: boolean; refreshDestination?: boolean; action: string }
> = {
  invalid_share_request: {
    renewedReview: true,
    action: 'Review the selected sessions again.',
  },
  unsupported_snapshot_version: {
    renewedReview: true,
    action: 'Update coSlash and build a new preview.',
  },
  snapshot_invalid: {
    renewedReview: true,
    action: 'Refresh the source and build a new preview.',
  },
  snapshot_too_large: {
    renewedReview: true,
    action: 'Reduce the source evidence and build a new preview.',
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
  consent_stale: {
    renewedReview: true,
    action: 'Review the changed source revision and approve it again.',
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

export function localSessionId(session: Pick<Session, 'agent' | 'id'>): string {
  return `${session.agent}:${session.id}`;
}

/** Hub candidates stay local-only before the existing agent:id sharing key. */
export function localShareCandidates(candidates: ShareCandidate[]): ShareCandidate[] {
  return candidates.filter(({ session }) => isLocalSession(session));
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

// Hidden approval is unsafe. Narrowing either filter removes newly hidden rows.
export function reconcileVisibleSelection(
  selected: ReadonlySet<string>,
  visible: ShareCandidate[],
): Set<string> {
  const visibleKeys = new Set(visible.map(({ session }) => localSessionId(session)));
  return new Set([...selected].filter((key) => visibleKeys.has(key)));
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
  const keys = candidates.map(({ session }) => localSessionId(session));
  const allSelected = keys.length > 0 && keys.every((key) => next.has(key));
  for (const key of keys) {
    if (allSelected) next.delete(key);
    else next.add(key);
  }
  return next;
}

export function bindPreviewConsent(
  session: Pick<Session, 'id' | 'agent' | 'mtime'>,
  preview: SnapshotPreview,
  destination: ShareDestination,
  idempotencyKey: string,
): ShareItemRequest {
  canonicalUploadBytes(preview);
  const contentHash = hubContentHash(preview);
  if (
    preview.adapterVersion !== 'snapshot-preview/v1' ||
    preview.sourceRevision !== session.mtime ||
    contentHash == null ||
    preview.payloadBytes == null
  ) {
    throw new Error('The preview no longer matches the selected source revision.');
  }
  if (destination.credentialState !== 'paired') {
    throw new Error('The destination credential is not ready.');
  }
  if (idempotencyKey.length < 16 || idempotencyKey.length > 200) {
    throw new Error('The idempotency key is outside the contract bounds.');
  }
  return {
    localSessionId: localSessionId(session),
    idempotencyKey,
    consent: {
      previewContractVersion: preview.adapterVersion,
      sourceRevision: preview.sourceRevision,
      contentHash,
      payloadBytes: preview.payloadBytes,
      destinationWorkspaceId: destination.workspaceId,
    },
  };
}

export function consentStillCurrent(
  item: ShareItemRequest,
  session: Pick<Session, 'id' | 'agent' | 'mtime'>,
  preview: SnapshotPreview,
  destination: ShareDestination,
): boolean {
  const hash = hubContentHash(preview);
  return (
    preview.state === 'ready' &&
    preview.approvalAllowed &&
    item.localSessionId === localSessionId(session) &&
    item.consent.previewContractVersion === preview.adapterVersion &&
    item.consent.sourceRevision === session.mtime &&
    item.consent.sourceRevision === preview.sourceRevision &&
    item.consent.contentHash === hash &&
    item.consent.payloadBytes === preview.payloadBytes &&
    item.consent.destinationWorkspaceId === destination.workspaceId &&
    destination.credentialState === 'paired'
  );
}

function hubContentHash(preview: SnapshotPreview): string | null {
  const value = preview.snapshot?.contentHash;
  const match = typeof value === 'string' ? /^sha256:([0-9a-f]{64})$/.exec(value) : null;
  return match?.[1] ?? null;
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
  return result.results.find((item) => item.state !== 'failed')?.route ?? null;
}

export function hubRouteURL(hubURL: string, path: string): string {
  return new URL(path.replace(/^\/+/, ''), `${hubURL.replace(/\/+$/, '')}/`).toString();
}
