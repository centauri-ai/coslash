// Shared spatial geometry for every Canvas board (Session workbench, DaGama
// pipeline, Atlas graph). A "box" is the persisted layout of one node; keeping
// it product-agnostic is what lets DaGama and Atlas reuse the geometry without
// importing Session Canvas.

export type CanvasNodeBox = {
  x: number;
  y: number;
  width: number;
  height: number;
  collapsed: boolean;
  locked: boolean;
};

export type CanvasWorld = { width: number; height: number };

// A collapsed node shrinks to just its header; both the node chrome and the wire
// routing use this height so wires stay attached to the visible box.
export const CANVAS_COLLAPSED_HEIGHT = 42;

/** Height the node actually occupies on screen — collapsed nodes are header-only. */
export function visibleHeight(box: CanvasNodeBox): number {
  return box.collapsed ? CANVAS_COLLAPSED_HEIGHT : box.height;
}

/**
 * Clamp a proposed top-left so the node stays fully inside the world.
 *
 * The upper bound uses the node's own width/height rather than the visible
 * height: a collapsed node that is dragged to the bottom edge must not jump
 * when it expands again.
 */
export function clampPosition(world: CanvasWorld, box: CanvasNodeBox, x: number, y: number) {
  return {
    x: Math.max(0, Math.min(world.width - box.width, x)),
    y: Math.max(0, Math.min(world.height - box.height, y)),
  };
}

/** Clamp a proposed size to the minimums and to the world edge from the node's origin. */
export function clampSize(
  world: CanvasWorld,
  box: CanvasNodeBox,
  minWidth: number,
  minHeight: number,
  width: number,
  height: number,
) {
  return {
    width: Math.max(minWidth, Math.min(world.width - box.x, width)),
    height: Math.max(minHeight, Math.min(world.height - box.y, height)),
  };
}

/** Drag result for `box`, given the pointer delta already divided by zoom. */
export function applyDrag(
  world: CanvasWorld,
  box: CanvasNodeBox,
  initial: CanvasNodeBox,
  dx: number,
  dy: number,
): CanvasNodeBox {
  const { x, y } = clampPosition(world, box, initial.x + dx, initial.y + dy);
  return { ...box, x, y };
}

/** Resize result for `box`, given the pointer delta already divided by zoom. */
export function applyResize(
  world: CanvasWorld,
  box: CanvasNodeBox,
  initial: CanvasNodeBox,
  minWidth: number,
  minHeight: number,
  dx: number,
  dy: number,
): CanvasNodeBox {
  const { width, height } = clampSize(
    world,
    box,
    minWidth,
    minHeight,
    initial.width + dx,
    initial.height + dy,
  );
  return { ...box, width, height };
}

// Pointer travel (in world units) past which a gesture counts as a drag rather
// than a click. Callers use this to suppress the click-select that browsers
// fire after a drag finishes.
export const CANVAS_DRAG_THRESHOLD = 3;

export function exceedsDragThreshold(dx: number, dy: number): boolean {
  return Math.abs(dx) + Math.abs(dy) > CANVAS_DRAG_THRESHOLD;
}
