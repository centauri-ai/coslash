import type { CanvasDestination } from '@/plugins/canvas/contracts';

// Readiness gating for Canvas destinations. An unready destination is hidden
// everywhere — navigation, deep links, and the renderer — so a half-migrated
// product is never reachable from the Log experience.

export type CanvasDestinationReadiness = Readonly<Record<CanvasDestination, boolean>>;

export function isDestinationReady(
  readiness: CanvasDestinationReadiness,
  destination: CanvasDestination,
): boolean {
  return readiness[destination] === true;
}

/** Destinations to show in navigation, in the given order, filtered by readiness. */
export function visibleDestinations(
  readiness: CanvasDestinationReadiness,
  order: readonly CanvasDestination[],
): readonly CanvasDestination[] {
  return order.filter((destination) => isDestinationReady(readiness, destination));
}

export function anyDestinationReady(readiness: CanvasDestinationReadiness): boolean {
  return Object.values(readiness).some(Boolean);
}

/**
 * Resolve the destination a board should actually render.
 *
 * A request for an unready destination resolves to `null` rather than falling
 * back to a ready sibling: silently redirecting would make an unfinished
 * product look like it exists under a different name.
 */
export function resolveDestination(
  readiness: CanvasDestinationReadiness,
  requested: CanvasDestination | null,
): CanvasDestination | null {
  if (requested === null) return null;
  return isDestinationReady(readiness, requested) ? requested : null;
}
