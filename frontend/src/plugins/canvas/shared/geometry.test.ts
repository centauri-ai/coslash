import { describe, expect, it } from 'vitest';
import {
  applyDrag,
  applyResize,
  CANVAS_COLLAPSED_HEIGHT,
  clampPosition,
  clampSize,
  exceedsDragThreshold,
  visibleHeight,
  type CanvasNodeBox,
  type CanvasWorld,
} from '@/plugins/canvas/shared/geometry';

const world: CanvasWorld = { width: 1000, height: 800 };

function box(overrides: Partial<CanvasNodeBox> = {}): CanvasNodeBox {
  return { x: 100, y: 100, width: 200, height: 150, collapsed: false, locked: false, ...overrides };
}

describe('visibleHeight', () => {
  it('uses the stored height when expanded', () => {
    expect(visibleHeight(box({ height: 320 }))).toBe(320);
  });

  it('shrinks to the header height when collapsed', () => {
    expect(visibleHeight(box({ height: 320, collapsed: true }))).toBe(CANVAS_COLLAPSED_HEIGHT);
  });
});

describe('clampPosition', () => {
  it('keeps a node inside the world', () => {
    expect(clampPosition(world, box(), 500, 400)).toEqual({ x: 500, y: 400 });
  });

  it('clamps past the right and bottom edges by the full box size', () => {
    expect(clampPosition(world, box(), 9999, 9999)).toEqual({ x: 800, y: 650 });
  });

  it('clamps negative coordinates to the origin', () => {
    expect(clampPosition(world, box(), -50, -50)).toEqual({ x: 0, y: 0 });
  });

  it('clamps a collapsed node by its expanded height so expanding never overflows', () => {
    const collapsed = box({ collapsed: true, height: 150 });
    expect(clampPosition(world, collapsed, 0, 9999).y).toBe(650);
  });
});

describe('clampSize', () => {
  it('honors the minimums', () => {
    expect(clampSize(world, box(), 120, 90, 10, 10)).toEqual({ width: 120, height: 90 });
  });

  it('stops growth at the world edge measured from the node origin', () => {
    expect(clampSize(world, box({ x: 700, y: 600 }), 120, 90, 9999, 9999)).toEqual({
      width: 300,
      height: 200,
    });
  });

  it('lets the minimum win when the node sits closer to the edge than the minimum', () => {
    expect(clampSize(world, box({ x: 990, y: 795 }), 120, 90, 500, 500)).toEqual({ width: 120, height: 90 });
  });
});

describe('applyDrag', () => {
  it('offsets from the gesture start, not the live box', () => {
    const initial = box();
    const live = box({ x: 400, y: 400 });
    expect(applyDrag(world, live, initial, 25, -30)).toMatchObject({ x: 125, y: 70 });
  });

  it('preserves collapsed and locked flags', () => {
    const initial = box({ collapsed: true, locked: true });
    expect(applyDrag(world, initial, initial, 10, 10)).toMatchObject({ collapsed: true, locked: true });
  });

  it('clamps the dragged result to the world', () => {
    const initial = box();
    expect(applyDrag(world, initial, initial, 5000, 5000)).toMatchObject({ x: 800, y: 650 });
  });
});

describe('applyResize', () => {
  it('offsets the size from the gesture start', () => {
    const initial = box();
    expect(applyResize(world, initial, initial, 120, 90, 40, 60)).toMatchObject({ width: 240, height: 210 });
  });

  it('never shrinks below the minimums', () => {
    const initial = box();
    expect(applyResize(world, initial, initial, 120, 90, -5000, -5000)).toMatchObject({
      width: 120,
      height: 90,
    });
  });

  it('leaves the origin untouched', () => {
    const initial = box();
    expect(applyResize(world, initial, initial, 120, 90, 40, 60)).toMatchObject({ x: 100, y: 100 });
  });
});

describe('exceedsDragThreshold', () => {
  it('treats tiny pointer jitter as a click', () => {
    expect(exceedsDragThreshold(1, 1)).toBe(false);
    expect(exceedsDragThreshold(-2, 1)).toBe(false);
  });

  it('treats travel beyond the threshold as a drag', () => {
    expect(exceedsDragThreshold(4, 0)).toBe(true);
    expect(exceedsDragThreshold(-3, -2)).toBe(true);
  });
});
