import { visibleHeight, type CanvasNodeBox } from '@/plugins/canvas/shared/geometry';

export type CanvasPoint = { x: number; y: number };

// Center of a node's visible box — the anchor a wire connects to. A collapsed
// node is only its header tall, so route to that instead of the stored height.
export function nodeCenter(box: CanvasNodeBox): CanvasPoint {
  return {
    x: box.x + box.width / 2,
    y: box.y + visibleHeight(box) / 2,
  };
}

/** Point on the visible box perimeter facing `toward` (so arrowheads sit outside cards). */
export function edgeAnchor(box: CanvasNodeBox, toward: CanvasPoint): CanvasPoint {
  const c = nodeCenter(box);
  const hw = box.width / 2;
  const hh = visibleHeight(box) / 2;
  const dx = toward.x - c.x;
  const dy = toward.y - c.y;
  if (dx === 0 && dy === 0) return { x: c.x + hw, y: c.y };
  const sx = dx !== 0 ? hw / Math.abs(dx) : Number.POSITIVE_INFINITY;
  const sy = dy !== 0 ? hh / Math.abs(dy) : Number.POSITIVE_INFINITY;
  const t = Math.min(sx, sy);
  return { x: c.x + dx * t, y: c.y + dy * t };
}

// A horizontal cubic bezier between two anchors; the bend scales with the
// horizontal gap so short and long links both read as smooth cables.
export function wirePath(start: CanvasPoint, end: CanvasPoint): string {
  const bend = Math.max(30, Math.abs(end.x - start.x) / 2);
  return `M${start.x} ${start.y} C${start.x + bend} ${start.y} ${end.x - bend} ${end.y} ${end.x} ${end.y}`;
}

/** Trigger wire from the exit edge of `from` to the entry edge of `to`. */
export function triggerWirePath(from: CanvasNodeBox, to: CanvasNodeBox): string {
  const fromCenter = nodeCenter(from);
  const toCenter = nodeCenter(to);
  return wirePath(edgeAnchor(from, toCenter), edgeAnchor(to, fromCenter));
}

/** Quadratic arc bowing below the seats so reverse feedback does not sit on the trigger. */
export function feedbackWirePath(start: CanvasPoint, end: CanvasPoint): string {
  const midX = (start.x + end.x) / 2;
  const bow = Math.max(56, Math.abs(end.x - start.x) * 0.22);
  const midY = Math.max(start.y, end.y) + bow;
  return `M${start.x} ${start.y} Q${midX} ${midY} ${end.x} ${end.y}`;
}

/** Feedback wire between box edges, bowed below so the reverse loop stays readable. */
export function feedbackBoxWirePath(from: CanvasNodeBox, to: CanvasNodeBox): string {
  // Prefer bottom-edge exits so the arc clears the cards.
  const start: CanvasPoint = { x: nodeCenter(from).x, y: from.y + visibleHeight(from) };
  const end: CanvasPoint = { x: nodeCenter(to).x, y: to.y + visibleHeight(to) };
  return feedbackWirePath(start, end);
}
