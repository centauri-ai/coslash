import { isLocalSession, type Session } from '@/pages/coslash/lib/session';

export type Group = {
  id: string;
  label: string;
  kind: 'Repository' | 'Folder' | 'No location';
  basis: string;
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

export function detectedGroup(session: Session): Group {
  const repo = session.repo?.trim() ?? '';
  if (repo && !session.repoLocalOnly) {
    const label = repo.split('/').filter(Boolean).at(-1) ?? repo;
    return {
      id: `repo:${repo.toLowerCase()}`,
      label,
      kind: 'Repository',
      basis: repo,
    };
  }
  const cwd = session.cwd.trim().replaceAll('\\', '/');
  const root = cwd ? folderRoot(cwd, repo) : repo;
  if (root) {
    const label = homeRelativeLabel(root);
    return {
      id: `folder:${session.sourceId}:${isLocalSession(session) ? root.toLowerCase() : root}`,
      label,
      kind: 'Folder',
      basis: label,
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
