import { FocusIcon, MinusIcon, PlusIcon } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  canZoomIn,
  canZoomOut,
  zoomIn,
  zoomOut,
  zoomPercent,
  type CanvasZoomBounds,
} from '@/plugins/canvas/shared/zoom';

// The bottom-left zoom cluster shared by canvas boards. Clamping/step math lives
// in zoom.ts; the caller supplies the current zoom, the bounds, and a reset
// handler (reset-to-100% on the workbench, fit-to-content on a graph board).
export function ZoomControls({
  zoom,
  bounds,
  onChange,
  onReset,
  resetLabel = 'Reset zoom',
}: {
  zoom: number;
  bounds: CanvasZoomBounds;
  onChange: (zoom: number) => void;
  onReset: () => void;
  resetLabel?: string;
}) {
  return (
    <div className="canvas-zoom-controls">
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={() => onChange(zoomOut(bounds, zoom))}
        disabled={!canZoomOut(bounds, zoom)}
        aria-label="Zoom out"
        title="Zoom out"
      >
        <MinusIcon />
      </Button>
      <span className="w-10 text-center text-[10px] font-semibold">{zoomPercent(zoom)}%</span>
      <Button
        variant="ghost"
        size="icon-sm"
        onClick={() => onChange(zoomIn(bounds, zoom))}
        disabled={!canZoomIn(bounds, zoom)}
        aria-label="Zoom in"
        title="Zoom in"
      >
        <PlusIcon />
      </Button>
      <span className="bg-border h-5 w-px" />
      <Button variant="ghost" size="icon-sm" onClick={onReset} aria-label={resetLabel} title={resetLabel}>
        <FocusIcon />
      </Button>
    </div>
  );
}
