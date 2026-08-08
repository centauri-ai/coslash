import { describe, expect, it } from 'vitest';
import { boardCommandFor, isTextEntryTarget, nodeCommandFor } from '@/plugins/canvas/shared/keyboard';

describe('boardCommandFor', () => {
  it('opens the command palette on the platform modifier plus K', () => {
    expect(boardCommandFor({ key: 'k', metaKey: true })).toBe('open-command-palette');
    expect(boardCommandFor({ key: 'K', ctrlKey: true })).toBe('open-command-palette');
  });

  it('maps the modifier zoom shortcuts, including the unshifted key faces', () => {
    expect(boardCommandFor({ key: '=', metaKey: true })).toBe('zoom-in');
    expect(boardCommandFor({ key: '+', metaKey: true })).toBe('zoom-in');
    expect(boardCommandFor({ key: '-', metaKey: true })).toBe('zoom-out');
    expect(boardCommandFor({ key: '_', metaKey: true })).toBe('zoom-out');
    expect(boardCommandFor({ key: '0', metaKey: true })).toBe('zoom-reset');
  });

  it('exits focus on Escape', () => {
    expect(boardCommandFor({ key: 'Escape' })).toBe('exit-focus');
  });

  it('ignores unmapped keys and Alt-modified chords', () => {
    expect(boardCommandFor({ key: 'q' })).toBeNull();
    expect(boardCommandFor({ key: '=', altKey: true })).toBeNull();
    expect(boardCommandFor({ key: 'j', metaKey: true })).toBeNull();
  });
});

describe('nodeCommandFor', () => {
  it('activates on Enter and Space', () => {
    expect(nodeCommandFor({ key: 'Enter' })).toBe('activate');
    expect(nodeCommandFor({ key: ' ' })).toBe('activate');
  });

  it('toggles collapse and lock on their letter keys, in either case', () => {
    expect(nodeCommandFor({ key: 'c' })).toBe('toggle-collapse');
    expect(nodeCommandFor({ key: 'C' })).toBe('toggle-collapse');
    expect(nodeCommandFor({ key: 'l' })).toBe('toggle-lock');
    expect(nodeCommandFor({ key: 'L' })).toBe('toggle-lock');
  });

  it('defers to the browser when a modifier is held, so shortcuts are not shadowed', () => {
    expect(nodeCommandFor({ key: 'c', metaKey: true })).toBeNull();
    expect(nodeCommandFor({ key: 'l', ctrlKey: true })).toBeNull();
  });

  it('ignores unmapped keys', () => {
    expect(nodeCommandFor({ key: 'Escape' })).toBeNull();
  });
});

describe('isTextEntryTarget', () => {
  it('recognizes form fields', () => {
    expect(isTextEntryTarget({ tagName: 'INPUT' })).toBe(true);
    expect(isTextEntryTarget({ tagName: 'textarea' })).toBe(true);
    expect(isTextEntryTarget({ tagName: 'SELECT' })).toBe(true);
  });

  it('recognizes contenteditable hosts', () => {
    expect(isTextEntryTarget({ tagName: 'DIV', isContentEditable: true })).toBe(true);
  });

  it('lets ordinary elements and a missing target through', () => {
    expect(isTextEntryTarget({ tagName: 'DIV' })).toBe(false);
    expect(isTextEntryTarget(null)).toBe(false);
  });
});
