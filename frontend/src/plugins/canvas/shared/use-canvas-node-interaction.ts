import { useEffect, useRef, type PointerEvent as ReactPointerEvent } from 'react';
import {
  applyDrag,
  applyResize,
  exceedsDragThreshold,
  type CanvasNodeBox,
  type CanvasWorld,
} from '@/plugins/canvas/shared/geometry';

// Pointer-driven drag/resize for canvas nodes, shared by all three products.
// Movement is divided by `zoom` so a node tracks the cursor 1:1 regardless of
// board scale, and the clamping rules live in geometry.ts so they can be tested
// without a DOM. `movedRef` lets the caller suppress the click-select that
// fires after a drag.
export function useCanvasNodeInteraction<Id extends string>({
  zoom,
  disabled,
  world,
  minWidth,
  minHeight,
  getLayout,
  updateLayout,
  onSelect,
  getCompanions,
}: {
  zoom: number;
  disabled: boolean;
  world: CanvasWorld;
  minWidth: number;
  minHeight: number;
  getLayout: (id: Id) => CanvasNodeBox;
  updateLayout: (id: Id, update: (layout: CanvasNodeBox) => CanvasNodeBox) => void;
  onSelect: (id: Id) => void;
  // Optional: ids that should be dragged in lockstep with `id` (e.g. an agent
  // terminal moving its bound note + log). Only consulted for a drag, not a
  // resize; each companion is clamped to the world independently.
  getCompanions?: (id: Id) => Id[];
}) {
  const movedRef = useRef(false);
  const cleanupRef = useRef<(() => void) | null>(null);

  // A drag in flight when the board unmounts would leak window listeners; tear
  // it down. cleanupRef is stable, so an empty-dep effect is correct here.
  useEffect(() => () => cleanupRef.current?.(), []);

  const start = (mode: 'drag' | 'resize', event: ReactPointerEvent, id: Id) => {
    const initial = getLayout(id);
    if (initial.locked || disabled || event.button !== 0) return;
    event.preventDefault();
    event.stopPropagation();
    const startX = event.clientX;
    const startY = event.clientY;
    cleanupRef.current?.();
    movedRef.current = false;
    onSelect(id);

    // Companions ride along on a drag by the same delta; snapshot their starting
    // boxes so each stays clamped to the world without drifting.
    const companions =
      mode === 'drag' && getCompanions
        ? getCompanions(id).map((companionId) => [companionId, getLayout(companionId)] as const)
        : [];

    const move = (pointer: PointerEvent) => {
      const dx = (pointer.clientX - startX) / zoom;
      const dy = (pointer.clientY - startY) / zoom;
      if (exceedsDragThreshold(dx, dy)) movedRef.current = true;
      updateLayout(id, (layout) =>
        mode === 'drag'
          ? applyDrag(world, layout, initial, dx, dy)
          : applyResize(world, layout, initial, minWidth, minHeight, dx, dy),
      );
      for (const [companionId, companionInitial] of companions) {
        updateLayout(companionId, (layout) => applyDrag(world, layout, companionInitial, dx, dy));
      }
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
      window.removeEventListener('pointercancel', stop);
      window.removeEventListener('blur', stop);
      cleanupRef.current = null;
      window.setTimeout(() => {
        movedRef.current = false;
      }, 0);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
    window.addEventListener('blur', stop);
    cleanupRef.current = stop;
  };

  return {
    movedRef,
    onDragStart: (event: ReactPointerEvent, id: Id) => start('drag', event, id),
    onResizeStart: (event: ReactPointerEvent, id: Id) => start('resize', event, id),
  };
}
