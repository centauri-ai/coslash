import { describe, expect, it } from 'vitest';
import {
  locationChoiceId,
  locationChoices,
  locationGroups,
  matchingLocation,
  sidebarGroups,
} from './detected-groups';
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
  it('shows a moved checkout once under its latest parent, retaining historical counts and saved observation filters', () => {
    const later = { ...checkout, repo: 'github.com/acme/new', lastSeen: 2 };
    const before = locationGroups(makeSession({ cwd: checkout.cwd, locations: [checkout] }));
    const after = locationGroups(makeSession({ cwd: checkout.cwd, locations: [later] }));
    const { repositories } = locationChoices(sidebarGroups([before, after]), 'fix-sidebar');
    expect(repositories.map(({ group }) => group.basis)).toEqual([later.repo]);
    const choice = locationChoiceId(repositories[0].worktrees[0]);
    expect(matchingLocation(before, [choice])?.context).toBe(checkout.repo);
    expect(matchingLocation(after, ['repo:' + checkout.repo, choice])).toBeUndefined();
  });

  it('places an originless main checkout under its observed root, and a pathless repository under Checkout unknown', () => {
    const main = { ...checkout, main: true, repo: '', root: '/local/project', cwd: '/local/project/sub' };
    const originless = locationGroups(makeSession({ cwd: main.cwd, locations: [main] }));
    const unknown = locationGroups(makeSession({ cwd: '', repo: checkout.repo }));
    const choices = locationChoices(sidebarGroups([unknown, originless]), '');
    expect(choices.folders.map((choice) => choice.label)).toEqual(['/local/project']);
    expect(choices.others).toHaveLength(0);
    const repo = choices.repositories[0];
    expect(matchingLocation(unknown, [locationChoiceId(repo.group)])).toBeUndefined();
    expect(matchingLocation(unknown, [repo.unknown!.id])).toBeDefined();
    expect(repo.worktrees).toHaveLength(0);
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
