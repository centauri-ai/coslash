import { useEffect, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import { searchParts, type SearchResults } from '@/pages/coslash/lib/search';

export function useRankedSearch(query: string) {
  const active = searchParts(query).content !== '';
  const [state, setState] = useState<{ query: string; state: string; results: SearchResults | null }>({
    query: '',
    state: 'checking',
    results: null,
  });
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function run() {
      try {
        const response = await apiFetch('/api/search/status', { signal: controller.signal });
        if (!response.ok) throw new Error('Search unavailable');
        const status = (await response.json()) as { state: string };
        if (controller.signal.aborted) return;
        setState({ query, state: status.state, results: null });
        if (!active) return;
        if (status.state !== 'ready') {
          timer = setTimeout(run, 5000);
          return;
        }
        const result = await apiFetch(`/api/search?q=${encodeURIComponent(query)}`, {
          signal: controller.signal,
        });
        if (!result.ok) throw new Error('Search failed');
        const results = (await result.json()) as SearchResults;
        if (!controller.signal.aborted) setState({ query, state: 'ready', results });
      } catch {
        if (!controller.signal.aborted) {
          setState({ query, state: 'unavailable', results: null });
          if (active) timer = setTimeout(run, 5000);
        }
      }
    }
    timer = setTimeout(run, active ? 250 : 0);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [query, active]);
  return state.query === query ? state : { query, state: 'checking', results: null };
}
