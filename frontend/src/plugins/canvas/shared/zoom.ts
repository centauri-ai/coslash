// Zoom clamping and stepping for canvas boards. Extracted from the control
// cluster so the math is testable without a DOM and so a board can drive zoom
// from a keyboard shortcut or a fit-to-content action using the same rules.

export type CanvasZoomBounds = {
  min: number;
  max: number;
  step: number;
};

export const CANVAS_DEFAULT_ZOOM_BOUNDS: CanvasZoomBounds = {
  min: 0.5,
  max: 1.5,
  step: 0.1,
};

// Zoom is rounded to two decimals at every step: repeatedly adding 0.1 in
// binary floating point otherwise drifts (0.7000000000000001) and leaks into
// the percentage label and persisted workspace state.
function round(zoom: number): number {
  return Number(zoom.toFixed(2));
}

export function clampZoom(bounds: CanvasZoomBounds, zoom: number): number {
  return round(Math.max(bounds.min, Math.min(bounds.max, zoom)));
}

export function zoomIn(bounds: CanvasZoomBounds, zoom: number): number {
  return clampZoom(bounds, round(zoom + bounds.step));
}

export function zoomOut(bounds: CanvasZoomBounds, zoom: number): number {
  return clampZoom(bounds, round(zoom - bounds.step));
}

export function canZoomIn(bounds: CanvasZoomBounds, zoom: number): boolean {
  return zoom < bounds.max;
}

export function canZoomOut(bounds: CanvasZoomBounds, zoom: number): boolean {
  return zoom > bounds.min;
}

/** Percentage shown in the zoom readout. */
export function zoomPercent(zoom: number): number {
  return Math.round(zoom * 100);
}

/**
 * Largest zoom that fits `world` inside `viewport`, clamped to the bounds.
 *
 * Boards that reset to fit-to-content (rather than to 100%) use this; a
 * degenerate viewport falls back to the minimum instead of producing 0 or NaN.
 */
export function fitZoom(
  bounds: CanvasZoomBounds,
  world: { width: number; height: number },
  viewport: { width: number; height: number },
): number {
  if (world.width <= 0 || world.height <= 0 || viewport.width <= 0 || viewport.height <= 0) {
    return clampZoom(bounds, bounds.min);
  }
  return clampZoom(bounds, Math.min(viewport.width / world.width, viewport.height / world.height));
}
