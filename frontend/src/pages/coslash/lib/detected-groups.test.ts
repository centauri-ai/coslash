import { describe, expect, it } from 'vitest';
import { locationGroups, sidebarGroups } from './detected-groups';
import { currentLocation, type SessionLocation } from './session';
import { makeSession } from './session-fixture';

const checkout: SessionLocation = {
  id: 'local-one',
  cloneId: 'clone',
  repo: 'github.com/centauri-ai/coslash-server',
  label: 'fix-sidebar',
  cwd: '/one/fix-sidebar',
  root: '/one/fix-sidebar',
  main: false,
  availability: 'available',
  headKind: 'unknown',
  lastSeen: 1,
};

describe('checkout labels', () => {
  it('distinguishes same-named checkouts and searches all their observed paths', () => {
    const rows = [
      checkout,
      { ...checkout, id: 'local-two', cwd: '/two/fix-sidebar', root: '/two/fix-sidebar' },
      { ...checkout, cwd: '/previous/fix-sidebar', root: '/previous/fix-sidebar', lastSeen: 0 },
    ].map((loc) => locationGroups(makeSession({ cwd: loc.cwd, locations: [loc] })));
    const choices = sidebarGroups(rows).filter((group) => group.kind === 'Worktree');
    expect(choices).toHaveLength(2);
    expect(choices[0].context).toContain('one/fix-sidebar');
    expect(choices[0].basis).toContain('/previous/fix-sidebar');
    expect(choices[1].context).toContain('two/fix-sidebar');
  });
  it('keeps a recorded repository and an unverified directory independently discoverable', () => {
    const groups = locationGroups(makeSession({ cwd: '/workspace/unknown', repo: checkout.repo }));
    expect(groups.options.map((group) => group.kind)).toEqual(['Repository', 'Folder']);
    expect(groups.options[1].label).toBe('/workspace/unknown');
  });

  it('uses the explicit primary marker when an SSH summary redacts directories', () => {
    const row = makeSession({
      sourceId: 'ssh',
      cwd: '',
      locations: [{ ...checkout, primary: false, cwd: '', root: '' }],
    });
    expect(currentLocation(row)).toBeUndefined();
    expect(currentLocation({ ...row, locations: [{ ...row.locations![0], primary: true }] })).toBeDefined();
  });
});
