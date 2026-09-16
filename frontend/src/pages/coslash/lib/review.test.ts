import { describe, expect, it } from 'vitest';
import {
  activeReviewForOrigin,
  availableReviewers,
  buildReviewLinks,
  reviewRequestPath,
  type ReviewableSession,
  type ReviewerOption,
} from '@/pages/coslash/lib/review';

const options: ReviewerOption[] = [
  { id: 'claude', label: 'Claude Code', available: true },
  { id: 'codex', label: 'Codex', available: true },
  { id: 'opencode', label: 'OpenCode', available: true },
];

function reviewable(overrides: Partial<ReviewableSession> = {}): ReviewableSession {
  return {
    sourceId: 'local',
    agent: 'claude',
    id: '12345678-aaaa-bbbb-cccc-123456789abc',
    name: 'Origin',
    mtime: 1,
    status: null,
    ...overrides,
  };
}

describe('availableReviewers', () => {
  it('prefers Codex and ranks the origin vendor last', () => {
    expect(availableReviewers(options, 'claude').map(({ id }) => id)).toEqual([
      'codex',
      'opencode',
      'claude',
    ]);
  });

  it('removes unavailable reviewers', () => {
    expect(
      availableReviewers(
        options.map((option) => (option.id === 'codex' ? { ...option, available: false } : option)),
        'claude',
      ).map(({ id }) => id),
    ).toEqual(['opencode', 'claude']);
  });
});

it('builds a source-aware review request', () => {
  expect(reviewRequestPath(reviewable(), 'codex')).toBe(
    '/api/reviews?source=local&id=12345678-aaaa-bbbb-cccc-123456789abc&reviewer=codex',
  );
});

describe('buildReviewLinks', () => {
  it('links a review to its origin and the origin to its newest review', () => {
    const origin = reviewable();
    const oldReview = reviewable({
      agent: 'codex',
      id: 'review-old',
      name: 'Review — Origin (12345678)',
      mtime: 2,
    });
    const newReview = reviewable({
      agent: 'opencode',
      id: 'review-new',
      name: 'Review — Origin (12345678)',
      mtime: 3,
      status: 'busy',
    });

    const links = buildReviewLinks([origin, oldReview, newReview]);

    expect(links.get('local:claude:12345678-aaaa-bbbb-cccc-123456789abc')).toEqual({
      kind: 'reviewed',
      target: newReview,
    });
    expect(links.get('local:codex:review-old')).toEqual({ kind: 'review', target: origin });
    expect(links.get('local:opencode:review-new')).toEqual({ kind: 'review', target: origin });
    expect(activeReviewForOrigin(origin, [origin, oldReview, newReview])).toBe(true);
  });

  it('does not link malformed or ambiguous names', () => {
    const first = reviewable({ id: '12345678-a' });
    const second = reviewable({ id: '12345678-b', agent: 'codex' });
    const review = reviewable({ id: 'review', name: 'Review — Origin (12345678)', agent: 'opencode' });
    expect(buildReviewLinks([first, second, review]).size).toBe(0);
  });
});
