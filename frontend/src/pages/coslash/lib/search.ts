import { useEffect, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  boardStatusKey,
  getVendor,
  isLocalSession,
  sessionKey,
  STATUSES,
  type Session,
} from '@/pages/coslash/lib/session';

const sessionSearchDocuments = new WeakMap<Session, string>();
const searchableDigestCategories = new Set(['first_prompt', 'user', 'question', 'recap', 'plan']);

function sessionSearchDocument(session: Session): string {
  const cached = sessionSearchDocuments.get(session);
  if (cached != null) return cached;

  const fields: (string | null | undefined)[] = [session.name, session.repo, session.branch, session.agent];
  for (const location of session.locations ?? [])
    fields.push(location.label, location.root, location.cwd, location.repo);
  if (isLocalSession(session)) {
    fields.push(session.firstPrompt, session.summary, session.declaredGoal);
    for (const entry of session.digest) {
      if (searchableDigestCategories.has(entry.category)) {
        fields.push(entry.description, entry.answer);
      }
    }
    if (session.synthesis != null) {
      fields.push(
        ...session.synthesis.goals,
        session.synthesis.outcome,
        ...session.synthesis.keyDecisions,
        ...(session.synthesis.decisions ?? []).flatMap((d) => [d.text, d.why, d.alternative]),
        session.synthesis.designDoc,
        session.synthesis.nextStep,
      );
    }
  }

  const document = fields
    .filter((value): value is string => value != null)
    .join('\n')
    .toLowerCase();
  sessionSearchDocuments.set(session, document);
  return document;
}

/** Remote sessions match on metadata only; local sessions also match their narrative text. */
export function sessionMatchesSearchTerm(session: Session, searchTerm: string): boolean {
  return sessionSearchDocument(session).includes(searchTerm.trim().toLowerCase());
}

/** Every word must match; known prefixes scope their field, including a `since:` timestamp. */
export function sessionMatchesSearch(session: Session, groupLabel: string, query: string): boolean {
  const words = searchTokens(query.trim().toLowerCase());
  return words.every((word) => {
    const prefix = word.match(/^(repo|worktree|group|machine|agent|status|since):(.+)$/);
    if (!prefix) return groupLabel.toLowerCase().includes(word) || sessionMatchesSearchTerm(session, word);
    const [, kind, rawValue] = prefix;
    const value = rawValue.replace(/^"|"$/g, '');
    if (kind === 'since') {
      const since = /^\d+$/.test(value) ? Number(value) : Date.parse(value);
      return Number.isFinite(since) && session.mtime >= since;
    }
    if (kind === 'repo')
      return [
        session.repoLocalOnly ? null : session.repo,
        ...(session.locations ?? []).map((loc) => loc.repo),
      ].some((repo) => repo?.toLowerCase().includes(value));
    if (kind === 'worktree')
      return (
        session.locations?.some((loc) =>
          [loc.main ? 'Main checkout' : loc.label, loc.root].some((label) =>
            label.toLowerCase().includes(value),
          ),
        ) ?? false
      );
    if (kind === 'group') return groupLabel.toLowerCase().includes(value);
    if (kind === 'machine') return session.sourceLabel.toLowerCase().includes(value);
    if (kind === 'agent') return getVendor(session.agent).label.toLowerCase().includes(value);
    return STATUSES[boardStatusKey(session)].label.toLowerCase().includes(value);
  });
}

export type SearchIdentity = { sourceId: string; agent: string; sessionId: string };
export type SearchMatch = { kind: string; turn?: number; snippet: string; ranges?: [number, number][] };
export type SearchHit = SearchIdentity & { score: number; coverage: string; match: SearchMatch };
export type DecisionHit = SearchIdentity & {
  kind: 'decision' | 'finding';
  status: 'proposed' | 'decided' | 'applied' | 'superseded';
  decisionId?: string;
  why?: string;
  alternative?: string;
  decidedBy?: '' | 'user' | 'agent_confirmed' | 'agent';
  text: string;
  turns: number[];
  cited: boolean;
  score: number;
};
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
    if (/^(repo|worktree|agent|machine|group|status|since):/i.test(token)) filters.push(token);
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
