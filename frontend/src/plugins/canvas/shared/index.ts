// Public surface of the shared Canvas layer. DaGama and Atlas import geometry,
// wires, interaction, and chrome from here without reaching into Session Canvas.

// Board chrome ships with the components so a product cannot render half-styled.
// Every selector is `.canvas-*` scoped, so loading this never touches Log.
import '@/plugins/canvas/shared/canvas.css';

export {
  CANVAS_COLLAPSED_HEIGHT,
  CANVAS_DRAG_THRESHOLD,
  applyDrag,
  applyResize,
  clampPosition,
  clampSize,
  exceedsDragThreshold,
  visibleHeight,
  type CanvasNodeBox,
  type CanvasWorld,
} from '@/plugins/canvas/shared/geometry';

export {
  edgeAnchor,
  feedbackBoxWirePath,
  feedbackWirePath,
  nodeCenter,
  triggerWirePath,
  wirePath,
  type CanvasPoint,
} from '@/plugins/canvas/shared/wire';

export {
  CANVAS_DEFAULT_ZOOM_BOUNDS,
  canZoomIn,
  canZoomOut,
  clampZoom,
  fitZoom,
  zoomIn,
  zoomOut,
  zoomPercent,
  type CanvasZoomBounds,
} from '@/plugins/canvas/shared/zoom';

export {
  anyDestinationReady,
  isDestinationReady,
  resolveDestination,
  visibleDestinations,
  type CanvasDestinationReadiness,
} from '@/plugins/canvas/shared/readiness';

export {
  boardCommandFor,
  isTextEntryTarget,
  nodeCommandFor,
  type CanvasCommand,
  type CanvasKeyDescriptor,
} from '@/plugins/canvas/shared/keyboard';

export { useCanvasNodeInteraction } from '@/plugins/canvas/shared/use-canvas-node-interaction';
export { CanvasNode } from '@/plugins/canvas/shared/CanvasNode';
export { ZoomControls } from '@/plugins/canvas/shared/ZoomControls';
export { CanvasStage, CanvasWires, CanvasWorldLayer } from '@/plugins/canvas/shared/CanvasStage';
export { CanvasCommandOverlay, CanvasInspector, CanvasSidePanel } from '@/plugins/canvas/shared/CanvasPanels';
