import {
  boardStatusKey,
  getVendor,
  isLocalSession,
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

/** Every word must match. A `repo:`, `worktree:`, `group:`, `machine:`, `agent:`, or `status:` word matches one field. */
export function sessionMatchesSearch(session: Session, groupLabel: string, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return words.every((word) => {
    const prefix = word.match(/^(repo|worktree|group|machine|agent|status):(.+)$/);
    if (!prefix) return groupLabel.toLowerCase().includes(word) || sessionMatchesSearchTerm(session, word);
    const [, kind, value] = prefix;
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
