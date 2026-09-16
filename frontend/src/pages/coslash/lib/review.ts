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
    id: origin.id,
    reviewer,
  })}`;
}

function originShortID(name: string | null): string | null {
  if (name == null) return null;
  return REVIEW_NAME.exec(name)?.[1] ?? null;
}

export function buildReviewLinks<T extends ReviewableSession>(
  sessions: readonly T[],
): Map<string, ReviewLink<T>> {
  const links = new Map<string, ReviewLink<T>>();
  const reviews = sessions.flatMap((candidate) => {
    const shortID = originShortID(candidate.name);
    return shortID == null ? [] : [{ session: candidate, shortID }];
  });
  const origins = sessions.filter((candidate) => originShortID(candidate.name) == null);

  for (const review of reviews) {
    const matches = origins.filter(
      (origin) => origin.sourceId === review.session.sourceId && origin.id.startsWith(review.shortID),
    );
    if (matches.length !== 1) continue;
    const origin = matches[0];
    links.set(sessionKey(review.session), { kind: 'review', target: origin });
    const current = links.get(sessionKey(origin));
    if (current == null || current.target.mtime < review.session.mtime) {
      links.set(sessionKey(origin), { kind: 'reviewed', target: review.session });
    }
  }
  return links;
}

export function activeReviewForOrigin(
  origin: ReviewableSession,
  sessions: readonly ReviewableSession[],
): boolean {
  return sessions.some((candidate) => {
    const shortID = originShortID(candidate.name);
    return (
      candidate.sourceId === origin.sourceId &&
      shortID != null &&
      origin.id.startsWith(shortID) &&
      (candidate.status === 'busy' || candidate.status === 'waiting')
    );
  });
}
