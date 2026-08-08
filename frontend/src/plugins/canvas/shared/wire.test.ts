import { describe, expect, it } from 'vitest';
import { CANVAS_COLLAPSED_HEIGHT, type CanvasNodeBox } from '@/plugins/canvas/shared/geometry';
import {
  edgeAnchor,
  feedbackBoxWirePath,
  feedbackWirePath,
  nodeCenter,
  triggerWirePath,
  wirePath,
} from '@/plugins/canvas/shared/wire';

function box(overrides: Partial<CanvasNodeBox> = {}): CanvasNodeBox {
  return { x: 0, y: 0, width: 200, height: 100, collapsed: false, locked: false, ...overrides };
}

describe('nodeCenter', () => {
  it('centers on the stored box when expanded', () => {
    expect(nodeCenter(box({ x: 100, y: 40 }))).toEqual({ x: 200, y: 90 });
  });

  it('centers on the header only when collapsed, so wires stay attached', () => {
    expect(nodeCenter(box({ x: 100, y: 40, collapsed: true }))).toEqual({
      x: 200,
      y: 40 + CANVAS_COLLAPSED_HEIGHT / 2,
    });
  });
});

describe('edgeAnchor', () => {
  it('lands on the right edge for a target directly to the right', () => {
    expect(edgeAnchor(box(), { x: 1000, y: 50 })).toEqual({ x: 200, y: 50 });
  });

  it('lands on the left edge for a target directly to the left', () => {
    expect(edgeAnchor(box(), { x: -1000, y: 50 })).toEqual({ x: 0, y: 50 });
  });

  it('lands on the bottom edge for a target directly below', () => {
    expect(edgeAnchor(box(), { x: 100, y: 1000 })).toEqual({ x: 100, y: 100 });
  });

  it('falls back to the right edge when the target is the center itself', () => {
    expect(edgeAnchor(box(), { x: 100, y: 50 })).toEqual({ x: 200, y: 50 });
  });

  it('stays on the perimeter for a diagonal target', () => {
    const anchor = edgeAnchor(box(), { x: 1000, y: 1000 });
    const onVerticalEdge = anchor.x === 0 || anchor.x === 200;
    const onHorizontalEdge = anchor.y === 0 || anchor.y === 100;
    expect(onVerticalEdge || onHorizontalEdge).toBe(true);
  });

  it('uses the collapsed height so the anchor tracks the visible box', () => {
    expect(edgeAnchor(box({ collapsed: true }), { x: 100, y: 1000 })).toEqual({
      x: 100,
      y: CANVAS_COLLAPSED_HEIGHT,
    });
  });
});

describe('wirePath', () => {
  it('emits a cubic bezier between the anchors', () => {
    expect(wirePath({ x: 0, y: 0 }, { x: 200, y: 100 })).toBe('M0 0 C100 0 100 100 200 100');
  });

  it('keeps a minimum bend so short links still read as cables', () => {
    expect(wirePath({ x: 0, y: 0 }, { x: 10, y: 0 })).toBe('M0 0 C30 0 -20 0 10 0');
  });
});

describe('triggerWirePath', () => {
  it('connects the facing edges of two boxes', () => {
    // Anchors land on the facing edges (200,50) -> (400,50); the bend is half
    // the 200px gap, so both control points sit at x=300.
    expect(triggerWirePath(box(), box({ x: 400 }))).toBe('M200 50 C300 50 300 50 400 50');
  });
});

describe('feedbackWirePath', () => {
  it('bows the quadratic arc below both endpoints', () => {
    expect(feedbackWirePath({ x: 0, y: 100 }, { x: 400, y: 100 })).toBe('M0 100 Q200 188 400 100');
  });

  it('applies a minimum bow for short spans', () => {
    expect(feedbackWirePath({ x: 0, y: 0 }, { x: 10, y: 0 })).toBe('M0 0 Q5 56 10 0');
  });
});

describe('feedbackBoxWirePath', () => {
  it('exits from the bottom edges so the arc clears the cards', () => {
    expect(feedbackBoxWirePath(box(), box({ x: 400 }))).toBe('M100 100 Q300 188 500 100');
  });

  it('exits from the collapsed header height when collapsed', () => {
    const path = feedbackBoxWirePath(box({ collapsed: true }), box({ x: 400, collapsed: true }));
    expect(path).toBe(
      `M100 ${CANVAS_COLLAPSED_HEIGHT} Q300 ${CANVAS_COLLAPSED_HEIGHT + 88} 500 ${CANVAS_COLLAPSED_HEIGHT}`,
    );
  });
});
