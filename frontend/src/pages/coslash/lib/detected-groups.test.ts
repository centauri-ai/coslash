import { describe, expect, it } from 'vitest';
import {
  locationChoiceId,
  locationChoices,
  locationGroups,
  matchingLocation,
  sidebarGroups,
} from './detected-groups';
import { sessionMatchesSearch } from './search';
import { currentLocation, type Session, type SessionLocation } from './session';
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

const primaryGroup = (session: Session) => locationGroups(session).primary;

function local(cwd: string, repo: string | null, overrides: Partial<Session> = {}): Session {
  return makeSession({ sourceId: 'local', cwd, repo, repoLocalOnly: true, ...overrides });
}

function remote(cwd: string, repo: string | null = null): Session {
  return makeSession({ sourceId: 'remote', cwd, repo });
}

describe('primary location group', () => {
  it('keeps same-named repositories from different owners apart', () => {
    const upstream = primaryGroup(
      local('/workspace/upstream', 'github.com/centauri-ai/coslash', { repoLocalOnly: false }),
    );
    const fork = primaryGroup(
      local('/workspace/fork', 'github.com/calvintvu/coslash', { repoLocalOnly: false }),
    );

    expect(upstream).toMatchObject({ kind: 'Repository', label: 'coslash' });
    expect(fork).toMatchObject({ kind: 'Repository', label: 'coslash' });
    expect(upstream.id).not.toBe(fork.id);
  });

  it('separates verified repositories from local-only folders', () => {
    const repository = local('/workspace/repos/centauri-ai', 'github.com/centauri-ai/centauri-ai', {
      repoLocalOnly: false,
    });

    expect(primaryGroup(repository)).toMatchObject({ kind: 'Repository', label: 'centauri-ai' });
    expect(primaryGroup(local('/workspace/folders/centauri-ai', 'centauri-ai'))).toMatchObject({
      kind: 'Folder',
      label: '/workspace/folders/centauri-ai',
    });
  });

  it('keeps same-named local folders apart by their path', () => {
    const client = primaryGroup(local('/home/milan/client/app', 'app'));
    const server = primaryGroup(local('/home/milan/server/app/src', 'app'));

    expect(client.label).toBe('~/client/app');
    expect(server.label).toBe('~/server/app');
    expect(client.id).not.toBe(server.id);
  });

  it.each([
    ['/home/milan/.codex/worktrees/0040/coslash', 'coslash', '~/.codex/worktrees/0040/coslash'],
    ['/tmp/smoke/app', 'app', '/tmp/smoke/app'],
    ['/Users/milan/Documents/Codex/2026-10-21/can-x20', 'can-x20', '~/Documents/Codex'],
    ['/Users/milan/Documents/Codex/2026-10-22/other/src', 'other', '~/Documents/Codex'],
    ['/home/milan/codex_work', null, '~/codex_work'],
    ['C:\\Users\\milan\\work\\api', 'api', '~/work/api'],
  ])('labels the local folder %s by its full path as %s', (cwd, repo, label) => {
    expect(primaryGroup(local(cwd, repo))).toMatchObject({ kind: 'Folder', label });
  });

  it('puts every Codex task folder in one group', () => {
    const ids = [
      local('/Users/milan/Documents/Codex/2026-10-21/can-x20', 'can-x20'),
      local('/Users/milan/Documents/Codex/2026-10-22/other/src', 'other'),
      local('/Users/milan/Documents/Codex/2026-10-23/another', 'another'),
    ].map((session) => primaryGroup(session).id);

    expect(new Set(ids)).toEqual(new Set(['folder:local:/users/milan/documents/codex']));
    expect(primaryGroup(local('/Users/milan/work/app', 'app')).id).not.toBe(ids[0]);
  });

  it('groups a remote working directory as a folder that group search matches', () => {
    const folder = remote('/home/milan/codex_work/finance_benchmark');
    const repository = remote('/workspace/app', 'github.com/centauri-ai/coslash');
    const folderGroup = primaryGroup(folder);
    const repositoryGroup = primaryGroup(repository);

    expect(folderGroup).toMatchObject({ kind: 'Folder', label: '~/codex_work/finance_benchmark' });
    expect(repositoryGroup).toMatchObject({ kind: 'Repository', label: 'coslash' });
    expect(sessionMatchesSearch(folder, folderGroup.label, 'group:finance_benchmark')).toBe(true);
    expect(sessionMatchesSearch(repository, repositoryGroup.label, 'group:finance_benchmark')).toBe(false);
  });

  it.each([
    ['/home/milan', '~'],
    ['/Users/milan', '~'],
    ['/root', '~'],
    ['/home/milan/projects/app', '~/projects/app'],
    ['/tmp', '/tmp'],
  ])('labels a non-repository SSH location %s as %s', (cwd, label) => {
    expect(primaryGroup(remote(cwd))).toMatchObject({ kind: 'Folder', label });
  });

  it('keeps case-sensitive remote folder groups distinct', () => {
    const upper = primaryGroup(remote('/home/milan/Foo'));
    const lower = primaryGroup(remote('/home/milan/foo'));

    expect(upper.label).toBe('~/Foo');
    expect(lower.label).toBe('~/foo');
    expect(upper.id).not.toBe(lower.id);
  });
});
