import { useEffect, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import { sessionKey, type DecisionRecord } from '@/pages/coslash/lib/session';

export type SearchIdentity = { sourceId: string; agent: string; sessionId: string };
export type SearchMatch = { kind: string; turn?: number; snippet: string; ranges?: [number, number][] };
export type SearchHit = SearchIdentity & { score: number; coverage: string; match: SearchMatch };
export type DecisionHit = SearchIdentity & DecisionRecord & { score: number };
export type SearchResults = {
  indexed?: SearchIdentity[];
  indexState: string;
  truncated: boolean;
  sessions: SearchHit[];
  decisions: DecisionHit[];
};
export function searchHitKey(hit: SearchIdentity): string {
  return sessionKey({ sourceId: hit.sourceId, agent: hit.agent, id: hit.sessionId });
}
export function searchTokens(query: string): string[] {
  return query.match(/(?:[^\s"]+|"[^"]*")+/g) ?? [];
}
export function searchParts(query: string): { content: string; filters: string } {
  const filters: string[] = [];
  const content: string[] = [];
  for (const token of searchTokens(query)) {
    if (/^(repo|agent|machine|group|status|since):/i.test(token)) filters.push(token);
    else if (/[\p{L}\p{N}]/u.test(token)) content.push(token);
  }
  return { content: content.join(' '), filters: filters.join(' ') };
}
export function canFallbackSearch(query: string): boolean {
  return !/"/.test(query) && !/\bsince:/i.test(query);
}
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
