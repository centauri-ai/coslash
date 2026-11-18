import { formatTimeAgo, formatTokens } from '@/pages/coslash/lib/format';
import type { SessionDetail } from '@/pages/coslash/lib/session';
import { HOUR } from '@/pages/coslash/lib/time';

export function contextFillReadiness(detail: SessionDetail): { value: string; tone: string } | null {
  if (detail.contextTokens == null) return null;
  if (detail.contextWindow == null) {
    return {
      value: `${formatTokens(detail.contextTokens)} used - window not available`,
      tone: 'text-coslash-muted',
    };
  }
  const pct = Math.round((detail.contextTokens / detail.contextWindow) * 100);
  return { value: `${pct}% of ${formatTokens(detail.contextWindow)}`, tone: fillTone(pct) };
}

export function branchDriftReadiness(git: SessionDetail['git']): { value: string; tone: string } | null {
  if (git == null) return null;
  return {
    value: `${git.ahead} ahead, ${git.behind} behind ${git.baseBranch}.`,
    tone: driftTone(git.behind),
  };
}

export function treeStaleReadiness(lastEditAt: number | null): { value: string; tone: string } | null {
  if (lastEditAt == null) return null;
  return { value: `Last edit ${formatTimeAgo(lastEditAt)}.`, tone: staleTone(Date.now() - lastEditAt) };
}

function fillTone(pct: number): string {
  if (pct >= 85) return 'text-danger-fg';
  if (pct >= 65) return 'text-warning-fg';
  return 'text-success-fg';
}

function driftTone(behind: number): string {
  if (behind > 15) return 'text-danger-fg';
  if (behind > 5) return 'text-warning-fg';
  return 'text-success-fg';
}

function staleTone(ageMs: number): string {
  if (ageMs > 72 * HOUR) return 'text-danger-fg';
  if (ageMs > 3 * HOUR) return 'text-warning-fg';
  return 'text-success-fg';
}
