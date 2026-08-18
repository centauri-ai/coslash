import { apiFetch } from '@/pages/coslash/lib/api';
import { RETRY_RULES, type DestinationResult, type ShareRequest, type ShareResult } from './model';

export type PairingResult = {
  state: 'pending' | 'paired' | 'expired';
  pairingId?: string;
  userCode?: string;
  verificationUri?: string;
  verificationUriComplete?: string;
  expiresAt?: string;
  intervalSeconds?: number;
};

type Guard<T> = (value: unknown) => value is T;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value != null && !Array.isArray(value);
}

function isOptionalString(value: unknown): boolean {
  return value == null || typeof value === 'string';
}

function isHubDestination(value: unknown): value is DestinationResult {
  if (
    !isRecord(value) ||
    value.contractVersion !== 'hub-share/v1' ||
    typeof value.configured !== 'boolean' ||
    !isOptionalString(value.hubUrl)
  ) {
    return false;
  }
  const states = ['signed_out', 'pairing_required', 'credential_dormant', 'credential_revoked'];
  if (typeof value.state !== 'string') return false;
  if (value.state !== 'ready') return states.includes(value.state) && value.destination == null;
  const destination = value.destination;
  return (
    isRecord(destination) &&
    typeof destination.workspaceId === 'string' &&
    typeof destination.workspaceName === 'string' &&
    typeof destination.currentMemberCount === 'number' &&
    typeof destination.resultingMemberCount === 'number' &&
    typeof destination.currentApprovedSessionCount === 'number' &&
    typeof destination.historyDisclosure === 'string' &&
    destination.credentialState === 'paired'
  );
}

function isPairingResult(value: unknown): value is PairingResult {
  return (
    isRecord(value) &&
    (value.state === 'pending' || value.state === 'paired' || value.state === 'expired') &&
    isOptionalString(value.pairingId) &&
    isOptionalString(value.userCode) &&
    isOptionalString(value.verificationUri) &&
    isOptionalString(value.verificationUriComplete) &&
    isOptionalString(value.expiresAt) &&
    (value.intervalSeconds == null || typeof value.intervalSeconds === 'number')
  );
}

function isShareResult(value: unknown): value is ShareResult {
  if (
    !isRecord(value) ||
    value.contractVersion !== 'hub-share/v1' ||
    typeof value.requestId !== 'string' ||
    !['succeeded', 'partial', 'failed'].includes(String(value.state)) ||
    !Array.isArray(value.results)
  ) {
    return false;
  }
  return value.results.every((item) => {
    if (
      !isRecord(item) ||
      typeof item.localSessionId !== 'string' ||
      typeof item.idempotencyKey !== 'string'
    ) {
      return false;
    }
    if (item.state === 'failed') {
      return (
        isRecord(item.error) &&
        typeof item.error.code === 'string' &&
        item.error.code in RETRY_RULES &&
        typeof item.error.retryable === 'boolean'
      );
    }
    return (
      (item.state === 'accepted' || item.state === 'already_accepted') &&
      typeof item.sessionId === 'string' &&
      typeof item.revisionId === 'string' &&
      typeof item.deduplicated === 'boolean' &&
      typeof item.sharedAt === 'string' &&
      typeof item.briefState === 'string' &&
      isRecord(item.route) &&
      item.route.hubContractVersion === 'hub-read/v1' &&
      typeof item.route.repositoryId === 'string' &&
      typeof item.route.canonicalWeekStart === 'string' &&
      typeof item.route.path === 'string'
    );
  });
}

async function jsonResponse<T>(response: Response, fallback: string, guard: Guard<T>): Promise<T> {
  if (!response.ok) throw new Error(`${fallback} (${response.status}).`);
  const value: unknown = await response.json();
  if (!guard(value)) throw new Error(`${fallback}: response is outside the expected contract.`);
  return value;
}

export async function loadHubDestination(): Promise<DestinationResult> {
  return jsonResponse<DestinationResult>(
    await apiFetch('/api/hub/destination'),
    'The Hub destination could not be loaded',
    isHubDestination,
  );
}

export async function beginHubPairing(): Promise<PairingResult> {
  return jsonResponse<PairingResult>(
    await apiFetch('/api/hub/pairings', { method: 'POST' }),
    'Device pairing could not start',
    isPairingResult,
  );
}

export async function pollHubPairing(pairingId: string): Promise<PairingResult> {
  return jsonResponse<PairingResult>(
    await apiFetch(`/api/hub/pairings/${encodeURIComponent(pairingId)}/poll`, { method: 'POST' }),
    'Device pairing could not finish',
    isPairingResult,
  );
}

export async function submitHubShare(request: ShareRequest): Promise<ShareResult> {
  return jsonResponse<ShareResult>(
    await apiFetch('/api/hub/shares', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(request),
    }),
    'The Hub share request failed',
    isShareResult,
  );
}
