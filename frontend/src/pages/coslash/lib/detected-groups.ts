import { currentLocation, isLocalSession, type Session } from './session';

export type Group = {
  id: string;
  label: string;
  kind: 'Repository' | 'Worktree' | 'Folder' | 'No location';
  basis: string;
  context?: string;
  source?: string;
  root?: string;
  checkout?: string;
  observed?: number;
  main?: boolean;
  repositoryId?: string;
};
/** Codex's dated task folders group under the `Documents/Codex` folder that holds them. */
const AGENT_TASK_FOLDER = /^(.*?(?:^|\/)Documents\/Codex)\/\d{4}-\d{2}-\d{2}\/[^/]+(?:\/|$)/;

/**
 * The folder a session without a remote groups under, the same rule as the Hub's personal groups: an
 * agent task folder's parent, else the working directory cut at the local repository's name.
 */
function folderRoot(cwd: string, repo: string): string {
  const agentFolder = AGENT_TASK_FOLDER.exec(cwd)?.[1];
  if (agentFolder) return agentFolder;
  const parts = cwd.split('/');
  const depth = repo ? parts.lastIndexOf(repo) : -1;
  return depth === -1 ? cwd : parts.slice(0, depth + 1).join('/');
}

/** `~/…` inside a recognizable home folder (`~` for home itself), else the full path, as on the Hub. */
function homeRelativeLabel(path: string): string {
  const parts = path.split('/').filter(Boolean);
  const inHome = (rest: string[]) => (rest.length ? `~/${rest.join('/')}` : '~');
  const drive = /^[A-Za-z]:$/.test(parts[0] ?? '');
  if (
    drive
      ? parts[1]?.toLowerCase() === 'users' && parts.length > 2
      : parts[0] === 'home' || parts[0] === 'Users'
  ) {
    return inHome(parts.slice(drive ? 3 : 2));
  }
  if (parts[0] === 'root') return inHome(parts.slice(1));
  return path;
}

function folderGroup(session: Session): Group {
  const repo = session.repo?.trim() ?? '';
  const cwd = session.cwd.trim().replaceAll('\\', '/');
  const name = session.repoLocalOnly ? repo : '';
  const root = cwd ? folderRoot(cwd, name) : name;
  if (root) {
    const label = homeRelativeLabel(root);
    return {
      id: `folder:${session.sourceId}:${isLocalSession(session) ? root.toLowerCase() : root}`,
      label,
      kind: 'Folder',
      basis: [label, cwd].join(' '),
    };
  }
  return {
    // One bucket, so the sidebar shows a single row rather than one per agent.
    id: 'unlocated',
    label: 'No location',
    kind: 'No location',
    basis: 'No repository or folder was recorded',
  };
}

export type SessionGroups = { primary: Group; locations: Group[][]; options: Group[] };

function repositoryGroup(repo: string): Group {
  return {
    id: `repo:${repo.toLowerCase()}`,
    label: repo.split('/').filter(Boolean).at(-1) ?? repo,
    kind: 'Repository',
    basis: repo,
  };
}

export function locationGroups(session: Session): SessionGroups {
  const repo = session.repo?.trim();
  const recordedRepo = repo && !session.repoLocalOnly ? repositoryGroup(repo) : undefined;
  const current = currentLocation(session);
  const locations = (session.locations ?? []).map((loc) => {
    const repo = loc.repo ? repositoryGroup(loc.repo) : undefined;
    const group: Group = {
      id: `worktree:${JSON.stringify([session.sourceId, loc.id])}`,
      label: loc.main ? 'Main checkout' : loc.label,
      context: loc.repo || session.sourceLabel,
      kind: 'Worktree',
      basis: [loc.root, loc.repo, session.sourceLabel, loc.label].filter(Boolean).join(' '),
      main: loc.main,
      observed: loc.lastSeen,
      source: session.sourceLabel,
      root: homeRelativeLabel(loc.root),
      checkout: loc.id,
      repositoryId: repo?.id,
    };
    return repo ? [group, repo] : [group];
  });
  const folder = folderGroup(session);
  const recorded = recordedRepo ?? folder;
  const primary = current && locations[0] ? locations[0][0] : recorded;
  if (!current) {
    const fallback = recordedRepo && folder.kind === 'Folder' ? [recordedRepo, folder] : [recorded];
    locations.unshift(fallback);
  } else if (recordedRepo && !locations[0].some((group) => group.id === recordedRepo.id)) {
    locations.push([recordedRepo]);
  }
  return {
    primary,
    locations,
    options: locations.flat(),
  };
}

export function matchesLocation(location: Group[], selected: string[], skipKind?: Group['kind']) {
  if (!selected.length) return true;
  const prefixes: [Group['kind'], string][] = [
    ['Repository', 'repo:'],
    ['Worktree', 'worktree:'],
    ['Folder', 'folder:'],
    ['No location', 'unlocated'],
  ];
  const required = prefixes
    .filter(([kind]) => kind !== skipKind)
    .map(([, prefix]) => selected.filter((id) => id.startsWith(prefix)))
    .filter((ids) => ids.length);
  if (selected.some((id) => !prefixes.some(([, prefix]) => id.startsWith(prefix)))) return false;
  return required.every((ids) => location.some((group) => ids.includes(group.id)));
}

export function matchingLocation(groups: SessionGroups, selected: string[]) {
  if (!selected.length) return groups.primary;
  const first = groups.locations.find((location) => matchesLocation(location, selected));
  return first?.[0];
}

/** Merge aliases across observations and distinguish names using only as much path as needed. */
export function sidebarGroups(sessions: Iterable<SessionGroups>): Group[] {
  const byId = new Map<string, Group>();
  const aliases = new Map<string, Set<string>>();
  for (const session of sessions)
    for (const group of session.options) {
      const previous = byId.get(group.id);
      if (!previous || (group.observed ?? 0) > (previous.observed ?? 0)) byId.set(group.id, group);
      const names = aliases.get(group.id) ?? new Set<string>();
      names.add(group.basis);
      aliases.set(group.id, names);
    }
  const byName = new Map<string, Group[]>();
  for (const group of byId.values()) {
    const name = JSON.stringify([group.kind, group.label, group.context]);
    const peers = byName.get(name) ?? [];
    peers.push(group);
    byName.set(name, peers);
  }
  const groups: Group[] = [];
  for (const peers of byName.values()) {
    const counts = new Map<string, number>();
    const options = peers.map((group) => {
      if (peers.length === 1) return [];
      if (!group.checkout) return [group.basis];
      const parts = (group.root ?? '').replaceAll('\\', '/').split('/').filter(Boolean);
      const names = [group.source ?? ''];
      for (let n = 1; n <= parts.length; n++)
        names.push([group.source, parts.slice(-n).join('/')].filter(Boolean).join(' · '));
      names.push([group.source, group.checkout].filter(Boolean).join(' · '));
      return [...new Set(names.filter(Boolean))];
    });
    for (const choices of options) for (const name of choices) counts.set(name, (counts.get(name) ?? 0) + 1);
    peers.forEach((group, i) => {
      const extra = options[i].find((name) => counts.get(name) === 1);
      groups.push({
        ...group,
        basis: [...aliases.get(group.id)!].join(' '),
        context: [group.context, extra].filter(Boolean).join(' · '),
      });
    });
  }
  return groups.sort((a, b) => a.label.localeCompare(b.label));
}
