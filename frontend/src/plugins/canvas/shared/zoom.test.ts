import { describe, expect, it } from 'vitest';
import {
  CANVAS_DEFAULT_ZOOM_BOUNDS,
  canZoomIn,
  canZoomOut,
  clampZoom,
  fitZoom,
  zoomIn,
  zoomOut,
  zoomPercent,
} from '@/plugins/canvas/shared/zoom';

const bounds = CANVAS_DEFAULT_ZOOM_BOUNDS;

describe('clampZoom', () => {
  it('passes through a value inside the bounds', () => {
    expect(clampZoom(bounds, 0.9)).toBe(0.9);
  });

  it('clamps to the bounds', () => {
    expect(clampZoom(bounds, 5)).toBe(bounds.max);
    expect(clampZoom(bounds, 0)).toBe(bounds.min);
  });
});

describe('zoomIn / zoomOut', () => {
  it('steps by the configured increment', () => {
    expect(zoomIn(bounds, 1)).toBe(1.1);
    expect(zoomOut(bounds, 1)).toBe(0.9);
  });

  it('stops at the bounds', () => {
    expect(zoomIn(bounds, bounds.max)).toBe(bounds.max);
    expect(zoomOut(bounds, bounds.min)).toBe(bounds.min);
  });

  it('does not accumulate floating-point drift across repeated steps', () => {
    let zoom = bounds.min;
    for (let step = 0; step < 10; step += 1) zoom = zoomIn(bounds, zoom);
    expect(zoom).toBe(bounds.max);

    let back = bounds.max;
    for (let step = 0; step < 10; step += 1) back = zoomOut(bounds, back);
    expect(back).toBe(bounds.min);
  });
});

describe('canZoomIn / canZoomOut', () => {
  it('reports headroom in each direction', () => {
    expect(canZoomIn(bounds, 1)).toBe(true);
    expect(canZoomOut(bounds, 1)).toBe(true);
  });

  it('reports no headroom at the bounds', () => {
    expect(canZoomIn(bounds, bounds.max)).toBe(false);
    expect(canZoomOut(bounds, bounds.min)).toBe(false);
  });
});

describe('zoomPercent', () => {
  it('renders a whole-number readout', () => {
    expect(zoomPercent(1)).toBe(100);
    expect(zoomPercent(0.85)).toBe(85);
    expect(zoomPercent(1.155)).toBe(116);
  });
});

describe('fitZoom', () => {
  it('picks the tighter of the two axes', () => {
    expect(fitZoom(bounds, { width: 1000, height: 1000 }, { width: 900, height: 700 })).toBe(0.7);
  });

  it('clamps the fit result to the bounds', () => {
    expect(fitZoom(bounds, { width: 100, height: 100 }, { width: 9000, height: 9000 })).toBe(bounds.max);
    expect(fitZoom(bounds, { width: 9000, height: 9000 }, { width: 100, height: 100 })).toBe(bounds.min);
  });

  it('falls back to the minimum for a degenerate viewport instead of 0 or NaN', () => {
    expect(fitZoom(bounds, { width: 1000, height: 1000 }, { width: 0, height: 0 })).toBe(bounds.min);
    expect(fitZoom(bounds, { width: 0, height: 0 }, { width: 500, height: 500 })).toBe(bounds.min);
  });
});
