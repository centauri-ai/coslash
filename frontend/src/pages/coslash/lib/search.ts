import { type Session } from '@/pages/coslash/lib/session';

export function sessionMatchesSearchTerm(session: Session, searchTerm: string): boolean {
  const term = searchTerm.trim().toLowerCase();
  if (term === '') return true;
  return [session.name, session.repo, session.branch].some(
    (field) => field != null && field.toLowerCase().includes(term),
  );
}
