import { createElement, Fragment } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import {
  AgentVendorFilterTabMenu,
  ALL_MACHINES,
  isMachineFilterValue,
  RepositoryFilterDropdownMenu,
  TimeWindowFilterTabMenu,
} from './CoslashTabMenus';
import type { MachineFact } from './lib/machines';

describe('isMachineFilterValue', () => {
  const machines: MachineFact[] = [
    { sourceId: 'r_0123456789abcdef', label: 'agent-box', state: 'ok', complete: true },
  ];

  it('accepts all, local, and an available remote machine', () => {
    expect(isMachineFilterValue(ALL_MACHINES, machines)).toBe(true);
    expect(isMachineFilterValue('local', machines)).toBe(true);
    expect(isMachineFilterValue('r_0123456789abcdef', machines)).toBe(true);
  });

  it('rejects an unknown machine', () => {
    expect(isMachineFilterValue('r_unknown', machines)).toBe(false);
  });
});

describe('filter dropdown menus', () => {
  it('renders the selected vendor and time-window labels as dropdown triggers', () => {
    const markup = renderToStaticMarkup(
      createElement(
        Fragment,
        null,
        createElement(AgentVendorFilterTabMenu, {
          value: 'codex',
          vendors: ['codex'],
          onValueChange: () => undefined,
        }),
        createElement(TimeWindowFilterTabMenu, { value: '30d', onValueChange: () => undefined }),
      ),
    );

    expect(markup).toContain('aria-haspopup="menu"');
    expect(markup.match(/lucide-chevron-down/g)).toHaveLength(2);
    expect(markup).toContain('Codex');
    expect(markup).toContain('30 days');
    expect(markup).not.toContain('role="tablist"');
  });

  it('keeps a long repository trigger bounded while preserving its full label', () => {
    const repository = 'github.com/centauri-ai/coslash';
    const markup = renderToStaticMarkup(
      createElement(RepositoryFilterDropdownMenu, {
        value: repository,
        repositories: [repository],
        onValueChange: () => undefined,
      }),
    );

    expect(markup).toContain(`aria-label="Repository: ${repository}"`);
    expect(markup).toContain(`title="${repository}"`);
    expect(markup).toContain('max-w-40');
    expect(markup).toContain('min-w-0');
    expect(markup).toContain('truncate');
  });
});
