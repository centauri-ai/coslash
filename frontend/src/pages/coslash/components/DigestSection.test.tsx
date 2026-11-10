// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it, vi } from 'vitest';
import { DigestSection } from '@/pages/coslash/components/SessionInspector';
import { decodeSession } from '@/pages/coslash/hooks/use-sessions';

it.each([
  { categories: ['first_prompt', 'user', 'compaction'] },
  { categories: ['user', 'compaction'] },
  { categories: ['user'] },
])('cycles ordinary clicks through only, excluded, and all: $categories', async ({ categories }) => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  const detail = decodeSession({
    agent: 'codex',
    id: 'filters',
    digest: categories.map((category, turn) => ({ category, turn, description: category })),
  });
  const mount = document.createElement('div');
  const root = createRoot(mount);
  const button = (label: string) =>
    [...mount.querySelectorAll('button')].find((button) => button.textContent === label)!;
  const visible = () => categories.filter((category) => mount.textContent!.includes(category));
  const click = async (label: string) => {
    await act(async () => button(label).click());
  };
  try {
    await act(async () => root.render(<DigestSection detail={detail} />));
    expect(visible()).toEqual(categories);
    expect(mount.querySelectorAll('button')).toHaveLength(categories.length);
    expect(mount.querySelectorAll('[aria-pressed="true"]')).toHaveLength(categories.length);
    await click('USER TURN1');
    expect(visible()).toEqual(['user']);
    expect(button('USER TURN1').getAttribute('aria-pressed')).toBe('true');
    await click('USER TURN1');
    expect(visible()).toEqual(categories.filter((category) => category !== 'user'));
    expect(button('USER TURN1').getAttribute('aria-pressed')).toBe('false');
    await click('USER TURN1');
    expect(visible()).toEqual(categories);
    expect(mount.querySelectorAll('[aria-pressed="true"]')).toHaveLength(categories.length);
    if (categories.length > 1) {
      await click('COMPACTION1');
      expect(visible()).toEqual(['compaction']);
      await click('COMPACTION1');
      await click('USER TURN1');
      expect(visible()).toEqual(['user']);
      await click('COMPACTION1');
      expect(visible()).toEqual(['compaction']);
    }
  } finally {
    await act(async () => root.unmount());
    vi.unstubAllGlobals();
  }
});
