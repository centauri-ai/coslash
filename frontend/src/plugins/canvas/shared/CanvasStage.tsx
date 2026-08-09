import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';
import type { CanvasWorld } from '@/plugins/canvas/shared/geometry';

// The full-bleed board shell: a scrolling dotted stage, a sticky toolbar row, a
// scaled world, and an SVG wire layer beneath the nodes. Products supply the
// toolbar, wires, and nodes; the shell owns scroll, scale, and stacking so all
// three boards behave identically.

export function CanvasStage({
  className,
  toolbar,
  children,
}: {
  className?: string;
  toolbar?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className={cn('canvas-stage', className)}>
      {toolbar !== undefined && <div className="canvas-toolbar">{toolbar}</div>}
      {children}
    </div>
  );
}

export function CanvasWorldLayer({
  world,
  zoom,
  hasFocus,
  className,
  children,
}: {
  world: CanvasWorld;
  zoom: number;
  // A focused node covers the board; wires are hidden so they do not cut across it.
  hasFocus: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      className="canvas-world-shell"
      // The shell reserves the scrolled area, so it must track the scaled world.
      style={{ width: world.width * zoom, height: world.height * zoom }}
    >
      <div
        className={cn('canvas-world', { 'canvas-world-has-focus': hasFocus }, className)}
        style={{ width: world.width, height: world.height, transform: `scale(${zoom})` }}
      >
        {children}
      </div>
    </div>
  );
}

export function CanvasWires({ world, children }: { world: CanvasWorld; children: ReactNode }) {
  return (
    <svg
      className="canvas-wires"
      viewBox={`0 0 ${world.width} ${world.height}`}
      // Decorative: the connections are also conveyed by the node content.
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}
