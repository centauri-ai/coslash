import { apiFetch } from '@/pages/coslash/lib/api';
import {
  FULL_SESSION_PREVIEW_VERSION,
  FULL_SESSION_SHARE_VERSION,
  type FullSessionPreview,
  type FullSessionSelection,
  type FullSessionShareError,
  type FullSessionShareRequest,
  type FullSessionShareResult,
} from './model';

function isRecord(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === 'object' && !Array.isArray(value);
}

const SHARE_ERRORS: FullSessionShareError[] = [
  'invalid_full_session_request',
  'full_session_invalid',
  'full_session_too_large',
  'incompatible_server',
  'review_binding_changed',
  'idempotency_conflict',
  'unauthorized',
  'rate_limited',
  'network_unavailable',
  'timeout',
  'temporary_unavailable',
];

function isSelection(value: unknown): value is FullSessionSelection {
  return (
    isRecord(value) &&
    typeof value.sourceId === 'string' &&
    typeof value.agent === 'string' &&
    typeof value.sessionId === 'string' &&
    typeof value.revisionId === 'string'
  );
}

function isPreview(value: unknown): value is FullSessionPreview {
  if (
    !isRecord(value) ||
    value.adapterVersion !== FULL_SESSION_PREVIEW_VERSION ||
    !['ready', 'invalid', 'stale_source', 'oversized', 'incompatible_server', 'unavailable'].includes(
      String(value.state),
    ) ||
    typeof value.approvalAllowed !== 'boolean' ||
    !isSelection(value.selection) ||
    typeof value.embeddedSecretRisk !== 'boolean'
  ) {
    return false;
  }
  if (value.state !== 'ready') {
    return isRecord(value.problem) && typeof value.problem.message === 'string' && value.envelope == null;
  }
  const envelope = value.envelope;
  return (
    value.approvalAllowed === true &&
    typeof value.recordBytes === 'number' &&
    typeof value.payloadBytes === 'number' &&
    typeof value.maxRecordBytes === 'number' &&
    /^sha256:[0-9a-f]{64}$/.test(String(value.recordSha256)) &&
    isRecord(envelope) &&
    envelope.schemaVersion === 'session-revision/v2' &&
    envelope.mediaType === 'application/vnd.coslash.session-revision.v2+json' &&
    envelope.recordByteCount === value.recordBytes &&
    envelope.recordSha256 === value.recordSha256 &&
    isRecord(envelope.repository) &&
    typeof envelope.repository.canonical === 'string' &&
    typeof envelope.repository.localOnly === 'boolean' &&
    isRecord(envelope.record)
  );
}

function isShareResult(value: unknown): value is FullSessionShareResult {
  if (
    !isRecord(value) ||
    value.contractVersion !== FULL_SESSION_SHARE_VERSION ||
    !['accepted', 'already_accepted', 'failed'].includes(String(value.state)) ||
    !isSelection(value.selection) ||
    typeof value.idempotencyKey !== 'string' ||
    typeof value.deduplicated !== 'boolean'
  ) {
    return false;
  }
  if (value.state === 'failed') {
    return (
      isRecord(value.error) &&
      SHARE_ERRORS.includes(value.error.code as FullSessionShareError) &&
      typeof value.error.retryable === 'boolean' &&
      value.route == null
    );
  }
  return (
    typeof value.repositoryId === 'string' &&
    typeof value.byteCount === 'number' &&
    /^sha256:[0-9a-f]{64}$/.test(String(value.contentSha256)) &&
    typeof value.sharedAt === 'string' &&
    isRecord(value.route) &&
    value.route.hubContractVersion === 'full-session-read/v1' &&
    typeof value.route.path === 'string'
  );
}

export async function fetchFullSessionPreview(selection: FullSessionSelection): Promise<FullSessionPreview> {
  const query = new URLSearchParams({
    source: selection.sourceId,
    agent: selection.agent,
    id: selection.sessionId,
    revision: selection.revisionId,
  });
  const response = await apiFetch(`/api/hub/full-session-preview?${query}`);
  if (!response.ok) throw new Error(`The full-session preview could not be loaded (${response.status}).`);
  const value: unknown = await response.json();
  if (!isPreview(value)) throw new Error('The full-session preview is outside the expected contract.');
  return value;
}

export async function submitFullSessionShare(
  request: FullSessionShareRequest,
): Promise<FullSessionShareResult> {
  const response = await apiFetch('/api/hub/full-session-shares', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  });
  if (!response.ok) throw new Error(`The full-session upload failed (${response.status}).`);
  const value: unknown = await response.json();
  if (!isShareResult(value)) throw new Error('The full-session result is outside the expected contract.');
  return value;
}
