import { describe, expect, it } from 'vitest';
import {
  DEFAULT_BOARD_FILTERS,
  loadBoardFilters,
  saveBoardFilters,
  vendorsForFilterMenu,
} from './board-filters';

function memoryStorage(initial: Record<string, string> = {}) {
  const data = { ...initial };
  return {
    getItem: (key: string) => data[key] ?? null,
    setItem: (key: string, value: string) => {
      data[key] = value;
    },
  };
}

describe('board filters', () => {
  it('defaults when nothing is stored', () => {
    expect(loadBoardFilters(memoryStorage())).toEqual(DEFAULT_BOARD_FILTERS);
  });

  it('round-trips vendor and timeframe', () => {
    const storage = memoryStorage();
    saveBoardFilters({ vendor: 'codex', timeWindow: 'all' }, storage);
    expect(loadBoardFilters(storage)).toEqual({ vendor: 'codex', timeWindow: 'all' });
  });

  it('falls back on corrupt or unknown values', () => {
    expect(loadBoardFilters(memoryStorage({ 'coslash.board-filters': '{' }))).toEqual(DEFAULT_BOARD_FILTERS);
    expect(
      loadBoardFilters(
        memoryStorage({ 'coslash.board-filters': JSON.stringify({ vendor: 'nope', timeWindow: 7 }) }),
      ),
    ).toEqual(DEFAULT_BOARD_FILTERS);
  });

  it('keeps a selected vendor visible when absent from results', () => {
    expect(vendorsForFilterMenu(['claude'], 'codex')).toEqual(['claude', 'codex']);
    expect(vendorsForFilterMenu(['claude'], 'claude')).toEqual(['claude']);
    expect(vendorsForFilterMenu(['claude'], 'all')).toEqual(['claude']);
  });
});
