import { useEffect, useState } from 'react';
import { apiFetch, readApiError } from '@/pages/coslash/lib/api';
import { isLocalSource } from '@/pages/coslash/lib/session';
import {
  decodeSynthesisCosts,
  synthesisCostsKey,
  synthesisCostsPath,
  type SynthesisCostsQuery,
  type SynthesisCostsResponse,
} from '@/pages/coslash/lib/synthesis-costs';

export async function loadSynthesisCosts(query: SynthesisCostsQuery, signal: AbortSignal) {
  const response = await apiFetch(synthesisCostsPath(query), { signal });
  if (!response.ok) {
    const detail = await readApiError(response);
    throw new Error(detail?.error ?? `Synthesis costs request failed (${response.status})`);
  }
  return decodeSynthesisCosts(await response.json());
}

type RequestState = {
  key: string | null;
  data: SynthesisCostsResponse | null;
  error: string | null;
  isLoading: boolean;
};

export function useSynthesisCosts(query: SynthesisCostsQuery | null) {
  const key = query && ('since' in query || isLocalSource(query.sourceId)) ? synthesisCostsKey(query) : null;
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<RequestState>({
    key: null,
    data: null,
    error: null,
    isLoading: false,
  });

  useEffect(() => {
    if (!query || !key) return;
    const controller = new AbortController();
    setState({ key, data: null, error: null, isLoading: true });
    loadSynthesisCosts(query, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setState({ key, data, error: null, isLoading: false });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setState({
            key,
            data: null,
            error: error instanceof Error ? error.message : 'Synthesis costs unavailable',
            isLoading: false,
          });
        }
      });
    return () => controller.abort();
  }, [key, attempt]);

  const current = state.key === key ? state : null;
  const refresh = () => setAttempt((value) => value + 1);
  return {
    data: current?.data ?? null,
    error: current?.error ?? null,
    isLoading: key !== null && (current?.isLoading ?? true),
    retry: refresh,
    refresh,
  };
}
