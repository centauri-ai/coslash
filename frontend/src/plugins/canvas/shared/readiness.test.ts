import { describe, expect, it } from 'vitest';
import { CANVAS_DESTINATIONS } from '@/plugins/canvas';
import {
  anyDestinationReady,
  isDestinationReady,
  resolveDestination,
  visibleDestinations,
  type CanvasDestinationReadiness,
} from '@/plugins/canvas/shared/readiness';

const none: CanvasDestinationReadiness = { canvas: false, dagama: false, atlas: false };
const canvasOnly: CanvasDestinationReadiness = { canvas: true, dagama: false, atlas: false };
const all: CanvasDestinationReadiness = { canvas: true, dagama: true, atlas: true };

describe('isDestinationReady', () => {
  it('reads the flag for the destination', () => {
    expect(isDestinationReady(canvasOnly, 'canvas')).toBe(true);
    expect(isDestinationReady(canvasOnly, 'dagama')).toBe(false);
  });
});

describe('visibleDestinations', () => {
  it('hides every destination while none are ready', () => {
    expect(visibleDestinations(none, CANVAS_DESTINATIONS)).toEqual([]);
  });

  it('shows only ready destinations and preserves the requested order', () => {
    expect(visibleDestinations(canvasOnly, CANVAS_DESTINATIONS)).toEqual(['canvas']);
    expect(visibleDestinations(all, ['atlas', 'canvas', 'dagama'])).toEqual(['atlas', 'canvas', 'dagama']);
  });
});

describe('anyDestinationReady', () => {
  it('is false while the whole suite is unready', () => {
    expect(anyDestinationReady(none)).toBe(false);
  });

  it('is true once any destination ships', () => {
    expect(anyDestinationReady(canvasOnly)).toBe(true);
  });
});

describe('resolveDestination', () => {
  it('resolves a ready destination to itself', () => {
    expect(resolveDestination(canvasOnly, 'canvas')).toBe('canvas');
  });

  it('resolves an unready destination to null rather than redirecting to a ready sibling', () => {
    expect(resolveDestination(canvasOnly, 'atlas')).toBeNull();
  });

  it('passes a null request through', () => {
    expect(resolveDestination(all, null)).toBeNull();
  });
});
