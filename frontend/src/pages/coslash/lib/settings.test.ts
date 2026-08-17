import { describe, expect, it } from 'vitest';
import { availableSynthesisBackends, type BackendOption } from '@/pages/coslash/lib/settings';

function backend(id: string, available: boolean): BackendOption {
  return { id, label: id, models: [], available };
}

describe('availableSynthesisBackends', () => {
  it('excludes undetected backends', () => {
    const options = [backend('claude-cli', true), backend('codex_exec', false)];

    expect(availableSynthesisBackends(options).map(({ id }) => id)).toEqual(['claude-cli']);
  });
});
