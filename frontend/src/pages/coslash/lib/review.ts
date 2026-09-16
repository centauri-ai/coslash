import { sessionKey, type SessionIdentity, type VendorKey } from '@/pages/coslash/lib/session';

export type ReviewerOption = {
  id: VendorKey;
  label: string;
  available: boolean;
};

export type ReviewableSession = SessionIdentity & {
  name: string | null;
  mtime: number;
  status: string | null;
};

export type ReviewLink<T extends ReviewableSession = ReviewableSession> = {
  kind: 'review' | 'reviewed';
  target: T;
};

export type ReviewIndex<T extends ReviewableSession = ReviewableSession> = {
  links: Map<string, ReviewLink<T>>;
  activeOrigins: Set<string>;
  reviewSessions: Set<string>;
};

const REVIEW_NAME = /^Review — .+ \(([^()]{8})\)$/;

export function availableReviewers(
  options: readonly ReviewerOption[],
  originAgent: string,
): ReviewerOption[] {
  return options
    .filter(({ available }) => available)
    .toSorted((left, right) => reviewerRank(left.id, originAgent) - reviewerRank(right.id, originAgent));
}

function reviewerRank(reviewer: string, originAgent: string): number {
  if (reviewer === originAgent) return 2;
  return reviewer === 'codex' ? 0 : 1;
}

export function reviewRequestPath(origin: SessionIdentity, reviewer: VendorKey): string {
  return `/api/reviews?${new URLSearchParams({
    source: origin.sourceId,
    agent: origin.agent,
    id: origin.id,
    reviewer,
  })}`;
}

function originShortID(name: string | null): string | null {
  if (name == null) return null;
  return REVIEW_NAME.exec(name)?.[1] ?? null;
}

export function buildReviewIndex<T extends ReviewableSession>(sessions: readonly T[]): ReviewIndex<T> {
  const links = new Map<string, ReviewLink<T>>();
  const activeOrigins = new Set<string>();
  const reviewSessions = new Set<string>();
  const origins = new Map<string, T | null>();
  const reviews: { session: T; shortID: string }[] = [];

  for (const candidate of sessions) {
    const shortID = originShortID(candidate.name);
    if (shortID != null) {
      reviewSessions.add(sessionKey(candidate));
      reviews.push({ session: candidate, shortID });
      continue;
    }
    const key = `${candidate.sourceId}:${candidate.id.slice(0, 8)}`;
    origins.set(key, origins.has(key) ? null : candidate);
  }

  for (const review of reviews) {
    const origin = origins.get(`${review.session.sourceId}:${review.shortID}`);
    if (origin == null) continue;
    links.set(sessionKey(review.session), { kind: 'review', target: origin });
    const current = links.get(sessionKey(origin));
    if (current == null || current.target.mtime < review.session.mtime) {
      links.set(sessionKey(origin), { kind: 'reviewed', target: review.session });
    }
    if (review.session.status === 'busy' || review.session.status === 'waiting') {
      activeOrigins.add(sessionKey(origin));
    }
  }
  return { links, activeOrigins, reviewSessions };
}
