import { machineRetryable, machineStatusText } from '@/pages/coslash/lib/machine-status';
import type { MachineFact } from '@/pages/coslash/lib/machines';
import {
  isLocalSession,
  sessionKey,
  type SessionIdentity,
  type VendorKey,
} from '@/pages/coslash/lib/session';

export type ReviewerOption = {
  id: VendorKey;
  label: string;
  available: boolean;
};

export function remoteReviewAvailability(
  machine: MachineFact | undefined,
  check: { state: 'ready' | 'offline' | 'error'; reviewers: readonly ReviewerOption[] } | null,
): { reason?: string; retryable: boolean } {
  if (machine && machine.state !== 'ok' && machine.state !== 'limited') {
    const guidance = machineRetryable(machine) ? ' Retry remote refresh to start a review.' : '';
    return { reason: machineStatusText(machine) + guidance, retryable: false };
  }
  if (check == null) return { reason: 'Checking reviewer CLIs on the SSH host.', retryable: false };
  if (check.state === 'offline') {
    return { reason: 'SSH host is offline. Reconnect it to start a review.', retryable: true };
  }
  if (check.state === 'error') {
    return { reason: 'Could not check reviewer CLIs on the SSH host. Retry the check.', retryable: true };
  }
  if (check.reviewers.length === 0) {
    return {
      reason: 'Install or update Claude Code CLI or Codex CLI on the SSH host, then retry the check.',
      retryable: true,
    };
  }
  return { retryable: false };
}

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

export function reviewerOptionsForOrigin(
  origin: SessionIdentity,
  local: readonly ReviewerOption[],
  remote: readonly ReviewerOption[],
): readonly ReviewerOption[] {
  return isLocalSession(origin) ? local : remote;
}

export function reviewActionVisible(
  session: { sourceId: string; cwd: string; launchable?: boolean },
  reviewSession: boolean,
): boolean {
  return (
    !reviewSession && (isLocalSession(session) ? session.cwd.trim() !== '' : session.launchable === true)
  );
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

export function isReviewSessionName(name: string | null): boolean {
  return originShortID(name) != null;
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
