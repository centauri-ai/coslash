import { describe, expect, it } from 'vitest';
import { detectedGroup } from '@/pages/coslash/lib/detected-groups';
import { sessionMatchesSearch } from '@/pages/coslash/lib/search';
import type { Session } from '@/pages/coslash/lib/session';
import { makeSession } from '@/pages/coslash/lib/session-fixture';

function local(cwd: string, repo: string | null, overrides: Partial<Session> = {}): Session {
  return makeSession({ sourceId: 'local', cwd, repo, repoLocalOnly: true, ...overrides });
}

function remote(cwd: string, repo: string | null = null): Session {
  return makeSession({ sourceId: 'remote', cwd, repo });
}

describe('detectedGroup', () => {
  it('keeps same-named repositories from different owners apart', () => {
    const upstream = detectedGroup(
      local('/workspace/upstream', 'github.com/centauri-ai/coslash', { repoLocalOnly: false }),
    );
    const fork = detectedGroup(
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

    expect(detectedGroup(repository)).toMatchObject({ kind: 'Repository', label: 'centauri-ai' });
    expect(detectedGroup(local('/workspace/folders/centauri-ai', 'centauri-ai'))).toMatchObject({
      kind: 'Folder',
      label: '/workspace/folders/centauri-ai',
    });
  });

  it('keeps same-named local folders apart by their path', () => {
    const client = detectedGroup(local('/home/milan/client/app', 'app'));
    const server = detectedGroup(local('/home/milan/server/app/src', 'app'));

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
    expect(detectedGroup(local(cwd, repo))).toMatchObject({ kind: 'Folder', label });
  });

  it('puts every Codex task folder in one group', () => {
    const ids = [
      local('/Users/milan/Documents/Codex/2026-10-21/can-x20', 'can-x20'),
      local('/Users/milan/Documents/Codex/2026-10-22/other/src', 'other'),
      local('/Users/milan/Documents/Codex/2026-10-23/another', 'another'),
    ].map((session) => detectedGroup(session).id);

    expect(new Set(ids)).toEqual(new Set(['folder:local:/users/milan/documents/codex']));
    expect(detectedGroup(local('/Users/milan/work/app', 'app')).id).not.toBe(ids[0]);
  });

  it('groups a remote working directory as a folder that group search matches', () => {
    const folder = remote('/home/milan/codex_work/finance_benchmark');
    const repository = remote('/workspace/app', 'github.com/centauri-ai/coslash');
    const folderGroup = detectedGroup(folder);
    const repositoryGroup = detectedGroup(repository);

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
    expect(detectedGroup(remote(cwd))).toMatchObject({ kind: 'Folder', label });
  });

  it('keeps case-sensitive remote folder groups distinct', () => {
    const upper = detectedGroup(remote('/home/milan/Foo'));
    const lower = detectedGroup(remote('/home/milan/foo'));

    expect(upper.label).toBe('~/Foo');
    expect(lower.label).toBe('~/foo');
    expect(upper.id).not.toBe(lower.id);
  });
});
