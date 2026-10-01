import { useCallback, useEffect, useState } from 'react';
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
  return loadSynthesisCostsPath(synthesisCostsPath(query), signal);
}

async function loadSynthesisCostsPath(path: string, signal: AbortSignal) {
  const response = await apiFetch(path, { signal });
  signal.throwIfAborted();
  if (!response.ok) {
    const detail = await readApiError(response);
    throw new Error(detail?.error ?? `Synthesis costs request failed (${response.status})`);
  }
  const body: unknown = await response.json();
  signal.throwIfAborted();
  return decodeSynthesisCosts(body);
}

type RequestState = {
  key: string | null;
  attempt: number;
  data: SynthesisCostsResponse | null;
  error: string | null;
};

export function currentSynthesisCostsState<T extends Pick<RequestState, 'key' | 'attempt'>>(
  state: T,
  key: string | null,
  attempt: number,
): T | null {
  return state.key === key && state.attempt === attempt ? state : null;
}

export function useSynthesisCosts(query: SynthesisCostsQuery | null) {
  const key = query && ('since' in query || isLocalSource(query.sourceId)) ? synthesisCostsKey(query) : null;
  const path = key && query ? synthesisCostsPath(query) : null;
  const [attempt, setAttempt] = useState(0);
  const [state, setState] = useState<RequestState>({
    key: null,
    attempt: -1,
    data: null,
    error: null,
  });

  useEffect(() => {
    if (!key || !path) return;
    const controller = new AbortController();
    loadSynthesisCostsPath(path, controller.signal)
      .then((data) => {
        if (!controller.signal.aborted) setState({ key, attempt, data, error: null });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setState({
            key,
            attempt,
            data: null,
            error: error instanceof Error ? error.message : 'Synthesis costs unavailable',
          });
        }
      });
    return () => controller.abort();
  }, [key, path, attempt]);

  const current = currentSynthesisCostsState(state, key, attempt);
  const refresh = useCallback(() => setAttempt((value) => value + 1), []);
  return {
    data: current?.data ?? null,
    error: current?.error ?? null,
    isLoading: key !== null && current === null,
    retry: refresh,
    refresh,
  };
}
