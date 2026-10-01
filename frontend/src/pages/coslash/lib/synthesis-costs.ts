import { isLocalSource, sessionKey, type SessionIdentity } from './session';

export type SynthesisCostsQuery = { since: number; until: number } | (SessionIdentity & { cursor?: string });

export type CostTotals = {
  knownCostMicroUsd: number | null;
  roundCount: number;
  invocationCount: number;
  unknownInvocationCount: number;
  incompleteRoundCount: number;
};

export type VendorCosts = { vendor: string; totals: CostTotals };
export type SynthesisRound = {
  id: string;
  sourceId: string;
  agent: string;
  sessionId: string;
  sourceRevision: number;
  startedAtMs: number;
  finishedAtMs: number | null;
  outcome: string;
  vendorModels: { vendor: string; model: string }[];
  totals: CostTotals;
  tokens: Record<
    string,
    {
      input_tokens: number;
      output_tokens: number;
      cache_creation_input_tokens: number;
      cache_creation_1h_input_tokens: number;
      cache_read_input_tokens: number;
      cost?: number;
    }
  >;
};

export type SynthesisCostsResponse = {
  sourceId: string;
  trackingStartedAtMs: number;
  historicalUnknown: boolean;
  totals: CostTotals;
  byVendor: VendorCosts[];
  rounds: SynthesisRound[];
  nextCursor: string | null;
};

export function synthesisCostsPath(query: SynthesisCostsQuery): string {
  const params = new URLSearchParams({ source: 'local' });
  if ('since' in query) {
    params.set('since', String(query.since));
    params.set('until', String(query.until));
  } else {
    if (!isLocalSource(query.sourceId)) throw new Error('Remote synthesis costs are not tracked here');
    params.set('agent', query.agent);
    params.set('id', query.id);
    if (query.cursor) params.set('cursor', query.cursor);
  }
  return `/api/synthesis-costs?${params}`;
}

export function synthesisCostsKey(query: SynthesisCostsQuery): string {
  return 'since' in query
    ? `month:${query.since}:${query.until}`
    : `session:${sessionKey(query)}:${query.cursor ?? ''}`;
}

export function synthesisCoverage(
  totals: Pick<CostTotals, 'knownCostMicroUsd' | 'unknownInvocationCount'> &
    Partial<Pick<CostTotals, 'incompleteRoundCount'>>,
) {
  if (totals.knownCostMicroUsd == null) return 'unknown';
  return totals.unknownInvocationCount > 0 || (totals.incompleteRoundCount ?? 0) > 0 ? 'partial' : 'complete';
}

export function combinedKnownCost(
  codingKnownUsd: number,
  synthesis: Pick<CostTotals, 'knownCostMicroUsd' | 'unknownInvocationCount'> &
    Partial<Pick<CostTotals, 'incompleteRoundCount'>>,
) {
  return {
    knownUsd: codingKnownUsd + (synthesis.knownCostMicroUsd ?? 0) / 1_000_000,
    partial: synthesisCoverage(synthesis) !== 'complete',
  };
}

function isTotals(value: unknown): value is CostTotals {
  if (!value || typeof value !== 'object') return false;
  const totals = value as Record<string, unknown>;
  return (
    (totals.knownCostMicroUsd === null ||
      (Number.isSafeInteger(totals.knownCostMicroUsd) && (totals.knownCostMicroUsd as number) >= 0)) &&
    ['roundCount', 'invocationCount', 'unknownInvocationCount', 'incompleteRoundCount'].every(
      (field) => Number.isSafeInteger(totals[field]) && (totals[field] as number) >= 0,
    )
  );
}

export function decodeSynthesisCosts(value: unknown): SynthesisCostsResponse {
  if (!value || typeof value !== 'object') throw new Error('Invalid synthesis costs response');
  const response = value as Record<string, unknown>;
  if (
    response.sourceId !== 'local' ||
    !Number.isSafeInteger(response.trackingStartedAtMs) ||
    typeof response.historicalUnknown !== 'boolean' ||
    !isTotals(response.totals) ||
    !Array.isArray(response.byVendor) ||
    !response.byVendor.every(
      (entry: unknown) =>
        !!entry &&
        typeof entry === 'object' &&
        typeof (entry as VendorCosts).vendor === 'string' &&
        isTotals((entry as VendorCosts).totals),
    ) ||
    !Array.isArray(response.rounds) ||
    (response.nextCursor !== null && typeof response.nextCursor !== 'string')
  )
    throw new Error('Invalid synthesis costs response');
  return response as SynthesisCostsResponse;
}
