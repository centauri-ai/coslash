/* oxlint-disable react/only-export-components -- The plugin boundary intentionally exports components and typed integration values. */
import type { ReactNode } from 'react';
import type { Session } from '@/pages/coslash/lib/session';
import type { CanvasDestination, CanvasSessionIdentity } from '@/plugins/canvas/contracts';

export type { CanvasDestination } from '@/plugins/canvas/contracts';
export * from '@/plugins/canvas/contracts';
export { FROZEN_CANVAS_CONTRACT_FIXTURES } from '@/plugins/canvas/fixtures';

export const CANVAS_DESTINATIONS = [
  'canvas',
  'dagama',
  'atlas',
] as const satisfies readonly CanvasDestination[];

export type CanvasDestinationReadiness = Readonly<Record<CanvasDestination, boolean>>;

// Destinations remain hidden until their implementation and integration gates pass.
export const CANVAS_DESTINATION_READINESS: CanvasDestinationReadiness = {
  canvas: false,
  dagama: false,
  atlas: false,
};

export type CanvasInspectorCallback = (session: CanvasSessionIdentity) => void;

export type CanvasDestinationNavigationProps = {
  current: CanvasDestination | null;
  readiness: CanvasDestinationReadiness;
  onSelect: (destination: CanvasDestination) => void;
};

export type CanvasDestinationRendererProps = {
  destination: CanvasDestination;
  sessions: readonly Session[];
  freshnessVersion: number;
  onInspectSession: CanvasInspectorCallback;
};

export type CanvasSessionCardActionProps = {
  session: CanvasSessionIdentity;
  selection: CanvasSessionIdentity | null;
  onSelect: (session: CanvasSessionIdentity) => void;
};

export type CanvasSettingsDiagnosticsMigrationProps = { onClose?: () => void };

// Compile-only entry components render nothing while all destinations are unready.
export function CanvasDestinationNavigation(_: CanvasDestinationNavigationProps): ReactNode {
  return null;
}

export function CanvasDestinationRenderer(_: CanvasDestinationRendererProps): ReactNode {
  return null;
}

export function CanvasSessionCardAction(_: CanvasSessionCardActionProps): ReactNode {
  return null;
}

export function CanvasSettingsDiagnosticsMigration(_: CanvasSettingsDiagnosticsMigrationProps): ReactNode {
  return null;
}
