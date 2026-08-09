// Keyboard command mapping shared by every Canvas board. Kept as a pure
// key-descriptor -> command function so the bindings are testable without a DOM
// and so all three products answer the same keys.

export type CanvasCommand =
  | 'activate'
  | 'exit-focus'
  | 'open-command-palette'
  | 'toggle-collapse'
  | 'toggle-lock'
  | 'zoom-in'
  | 'zoom-out'
  | 'zoom-reset';

export type CanvasKeyDescriptor = {
  key: string;
  metaKey?: boolean;
  ctrlKey?: boolean;
  shiftKey?: boolean;
  altKey?: boolean;
};

/**
 * Board-level bindings — the ones that fire while the stage (not a node) has
 * focus. Typing inside an input must never reach these; `isTextEntryTarget`
 * below is the guard callers apply first.
 */
export function boardCommandFor(event: CanvasKeyDescriptor): CanvasCommand | null {
  const mod = event.metaKey === true || event.ctrlKey === true;
  if (mod && event.key.toLowerCase() === 'k') return 'open-command-palette';
  if (event.altKey === true) return null;
  if (mod) {
    if (event.key === '=' || event.key === '+') return 'zoom-in';
    if (event.key === '-' || event.key === '_') return 'zoom-out';
    if (event.key === '0') return 'zoom-reset';
    return null;
  }
  if (event.key === 'Escape') return 'exit-focus';
  return null;
}

/**
 * Node-level bindings — fired only when the event target is the node chrome
 * itself, so a button or textarea inside the body keeps its own behavior.
 */
export function nodeCommandFor(event: CanvasKeyDescriptor): CanvasCommand | null {
  if (event.metaKey === true || event.ctrlKey === true || event.altKey === true) return null;
  if (event.key === 'Enter' || event.key === ' ') return 'activate';
  if (event.key.toLowerCase() === 'c') return 'toggle-collapse';
  if (event.key.toLowerCase() === 'l') return 'toggle-lock';
  return null;
}

const TEXT_ENTRY_TAGS = new Set(['INPUT', 'TEXTAREA', 'SELECT']);

/** True when the event originated in a field where keystrokes are literal text. */
export function isTextEntryTarget(target: { tagName?: string; isContentEditable?: boolean } | null): boolean {
  if (target === null) return false;
  if (target.isContentEditable === true) return true;
  return target.tagName !== undefined && TEXT_ENTRY_TAGS.has(target.tagName.toUpperCase());
}
