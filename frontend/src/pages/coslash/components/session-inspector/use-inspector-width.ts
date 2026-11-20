import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from 'react';

const INSPECTOR_WIDTH_KEY = 'coslash.inspector-width.v1';
const MIN_INSPECTOR_WIDTH = 360;
const MAX_INSPECTOR_VIEWPORT_SHARE = 0.8;
const DEFAULT_INSPECTOR_WIDTH = 'clamp(420px, 32vw, 760px)';
const INSPECTOR_KEYBOARD_STEP = 16;

function maximumInspectorWidth(viewportWidth: number): number {
  return Math.max(MIN_INSPECTOR_WIDTH, Math.round(viewportWidth * MAX_INSPECTOR_VIEWPORT_SHARE));
}

function clampInspectorWidth(width: number, viewportWidth = window.innerWidth): number {
  return Math.round(Math.min(Math.max(width, MIN_INSPECTOR_WIDTH), maximumInspectorWidth(viewportWidth)));
}

function defaultInspectorWidth(viewportWidth: number): number {
  return clampInspectorWidth(Math.min(Math.max(viewportWidth * 0.32, 420), 760), viewportWidth);
}

export function inspectorWidthForKey(
  key: string,
  currentWidth: number,
  viewportWidth: number,
): number | null {
  if (key === 'Home') return MIN_INSPECTOR_WIDTH;
  if (key === 'End') return maximumInspectorWidth(viewportWidth);
  if (key === 'ArrowLeft') {
    return clampInspectorWidth(currentWidth + INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  if (key === 'ArrowRight') {
    return clampInspectorWidth(currentWidth - INSPECTOR_KEYBOARD_STEP, viewportWidth);
  }
  return null;
}

function readInspectorWidth(): number | null {
  try {
    const stored = Number(window.localStorage.getItem(INSPECTOR_WIDTH_KEY));
    return Number.isFinite(stored) && stored > 0 ? Math.max(stored, MIN_INSPECTOR_WIDTH) : null;
  } catch {
    return null;
  }
}

/**
 * The inspector width: pointer and keyboard resizing on the separator, the stored width, and the
 * `--coslash-inspector-width` variable that keeps the session list clear of the open sheet.
 */
export function useInspectorWidth(contentRef: RefObject<HTMLDivElement | null>, isOpen: boolean) {
  const resizeRef = useRef<{
    pointerId: number;
    startX: number;
    startWidth: number;
    currentWidth: number;
  } | null>(null);
  const [width, setWidth] = useState<number | null>(readInspectorWidth);
  const [viewportWidth, setViewportWidth] = useState(() =>
    typeof window === 'undefined' ? 1375 : window.innerWidth,
  );

  const persistWidth = (nextWidth: number) => {
    setWidth(nextWidth);
    try {
      window.localStorage.setItem(INSPECTOR_WIDTH_KEY, String(nextWidth));
    } catch {
      // a blocked store just means the width resets next session
    }
  };

  const startResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    event.currentTarget.setPointerCapture(event.pointerId);
    const startWidth = clampInspectorWidth(contentRef.current?.offsetWidth ?? MIN_INSPECTOR_WIDTH);
    resizeRef.current = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startWidth,
      currentWidth: startWidth,
    };
  };
  const continueResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resize.currentWidth = clampInspectorWidth(resize.startWidth + resize.startX - event.clientX);
    setWidth(resize.currentWidth);
  };
  const finishResize = (event: ReactPointerEvent<HTMLDivElement>) => {
    const resize = resizeRef.current;
    if (resize == null || resize.pointerId !== event.pointerId) return;
    resizeRef.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    persistWidth(resize.currentWidth);
  };
  const resizeWithKeyboard = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const currentWidth = contentRef.current?.offsetWidth ?? width ?? MIN_INSPECTOR_WIDTH;
    const nextWidth = inspectorWidthForKey(event.key, currentWidth, viewportWidth);
    if (nextWidth == null) return;
    event.preventDefault();
    persistWidth(nextWidth);
  };

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const updateViewportWidth = () => setViewportWidth(window.innerWidth);
    window.addEventListener('resize', updateViewportWidth);
    return () => window.removeEventListener('resize', updateViewportWidth);
  }, []);

  const inspectorWidth =
    width == null ? DEFAULT_INSPECTOR_WIDTH : `min(${width}px, ${MAX_INSPECTOR_VIEWPORT_SHARE * 100}vw)`;

  useEffect(() => {
    if (!isOpen) return;
    const root = document.documentElement;
    root.style.setProperty('--coslash-inspector-width', inspectorWidth);
    return () => {
      root.style.removeProperty('--coslash-inspector-width');
    };
  }, [isOpen, inspectorWidth]);

  return {
    inspectorWidth,
    separatorProps: {
      'aria-valuemin': MIN_INSPECTOR_WIDTH,
      'aria-valuemax': maximumInspectorWidth(viewportWidth),
      'aria-valuenow': clampInspectorWidth(width ?? defaultInspectorWidth(viewportWidth), viewportWidth),
      'onPointerDown': startResize,
      'onPointerMove': continueResize,
      'onPointerUp': finishResize,
      'onPointerCancel': finishResize,
      'onLostPointerCapture': finishResize,
      'onKeyDown': resizeWithKeyboard,
    },
  };
}
