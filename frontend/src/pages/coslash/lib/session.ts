export type ModelTokens = {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_creation_1h_input_tokens: number;
  cache_read_input_tokens: number;
  cost?: number;
};

export type SessionSynthesis = {
  goals: string[];
  outcome: string;
  keyDecisions: string[];
  nextStep: string;
};

export type SubagentCommand = { label: string; command: string };

export type Subagent = {
  id: string;
  name: string;
  model: string | null;
  status: 'running' | 'returned' | 'aborted';
  task: string;
  result: string;
  durationMs: number | null;
  spawnedAtTurn: number | null;
  toolUses: number;
  commands: SubagentCommand[];
  tokens: Record<string, ModelTokens>;
  cost: number;
};

export const LOCAL_SOURCE_ID = 'local';
export const LOCAL_SOURCE_LABEL = 'This Mac';

export type Session = {
  sourceId: string;
  sourceLabel: string;
  eligibleForAggregates: boolean;
  displayStale: boolean;
  launchable?: boolean;
  lastSeenStatus?: string;
  agent: string;
  id: string;
  name: string | null;
  summary: string | null;
  status: string | null;
  cwd: string;
  branch: string | null;
  repo: string | null;
  repoLocalOnly: boolean;
  files: number;
  durationMs: number | null;
  tokens: Record<string, ModelTokens>;
  cost: number;
  unpricedModels: string[];
  subagents: Subagent[];
  mtime: number;
  entrypoint: string | null;
  synthesis: SessionSynthesis | null;
  synthesisPending: boolean;
  synthesisError?: string;
  declaredGoal: string | null;
  model: string | null;
  contextTokens: number | null;
  contextWindow: number | null;
  turns: number;
  toolUses: number;
  errors: number;
  compactions: number;
  firstPrompt: string | null;
  commands: string[];
  commits: string[];
  prs: number;
  todos: { text: string; done: boolean }[];
  digest: DigestEntry[];
  fileEdits: FileEdit[];
  git: GitDrift | null;
  lastEditAt: number | null;
};

export type SessionIdentity = Pick<Session, 'sourceId' | 'agent' | 'id'>;

export function sessionKey(session: SessionIdentity): string {
  return `${session.sourceId}:${session.agent}:${session.id}`;
}

export function sameSession(a: SessionIdentity, b: SessionIdentity): boolean {
  return a.sourceId === b.sourceId && a.agent === b.agent && a.id === b.id;
}

export function isLocalSource(sourceId: string): boolean {
  return sourceId === LOCAL_SOURCE_ID;
}

export function isLocalSession(session: Pick<Session, 'sourceId'>): boolean {
  return isLocalSource(session.sourceId);
}

export function sessionsForAggregates<T extends Pick<Session, 'eligibleForAggregates'>>(
  sessions: readonly T[],
): T[] {
  return sessions.filter((session) => session.eligibleForAggregates);
}

/** Missing or blank remote env facts render as an em dash, never `undefined`. */
export function environmentFact(value: string | null | undefined): string {
  const trimmed = value?.trim();
  return trimmed ? trimmed : '—';
}

export function withLocalSourceDefaults<T extends { agent: string; id: string }>(
  session: T,
): T & Pick<Session, 'sourceId' | 'sourceLabel' | 'eligibleForAggregates' | 'displayStale'> {
  const record = session as T & Partial<Session>;
  return {
    ...session,
    sourceId: record.sourceId ?? LOCAL_SOURCE_ID,
    sourceLabel: record.sourceLabel ?? LOCAL_SOURCE_LABEL,
    eligibleForAggregates: record.eligibleForAggregates ?? true,
    displayStale: record.displayStale ?? false,
  };
}

type FileEdit = {
  path: string;
  adds: number;
  dels: number;
  edits: number;
  isNew: boolean;
};

type GitDrift = { baseBranch: string; ahead: number; behind: number };

export type DigestEntry = {
  turn: number;
  category: 'first_prompt' | 'user' | 'question' | 'todos' | 'compaction' | 'recap' | 'subagent';
  description: string;
  answer?: string;
  subagentId?: string;
  time?: number;
};

export type SessionDetail = Session;

export type GoalSource = 'declared' | 'inferred' | 'floor';

export function isSynthesisEligible(
  session: Pick<Session, 'turns' | 'compactions' | 'contextTokens'>,
): boolean {
  return session.turns > 5 || session.compactions > 0 || (session.contextTokens ?? 0) > 100_000;
}

export function goalSourceLabel(source: GoalSource): string {
  return source === 'floor' ? 'first prompt' : source;
}

export function resolveGoal(
  session: Pick<Session, 'declaredGoal' | 'synthesis'> & { firstPrompt?: string | null },
): { texts: string[]; source: GoalSource } {
  if (session.declaredGoal?.trim()) {
    return { texts: [session.declaredGoal], source: 'declared' };
  }
  const goals = session.synthesis?.goals.filter((goal) => goal.trim()) ?? [];
  if (goals.length > 0) {
    return { texts: goals, source: 'inferred' };
  }
  return { texts: [session.firstPrompt?.trim() || '—'], source: 'floor' };
}

export function firstSentence(text: string | null | undefined): string | null {
  const trimmed = text?.trim();
  if (!trimmed) return null;
  const sentence = trimmed.match(/^.*?[.!?](?=\s|$)/s);
  return sentence?.[0] ?? trimmed;
}

export function getSessionOutcome(session: Pick<Session, 'synthesis' | 'summary'>): string | null {
  const synthesizedOutcome = session.synthesis?.outcome.trim();
  if (synthesizedOutcome) return synthesizedOutcome;
  return session.summary?.trim() || null;
}

export function getSessionCardSummary(
  session: Pick<Session, 'synthesis' | 'summary' | 'synthesisPending'>,
): string {
  const summary = firstSentence(getSessionOutcome(session));
  if (summary) return summary;
  return session.synthesisPending ? 'Summary still generating' : 'No summary available';
}

export type Vendor = { label: string; mono: string; fg: string; bg: string };
export type Status = { label: string; fg: string; bg: string; dot: string };

const VENDORS = {
  claude: {
    label: 'Claude Code',
    mono: 'CC',
    fg: 'text-claude',
    bg: 'bg-claude-bg',
  },
  codex: { label: 'Codex', mono: 'CX', fg: 'text-codex', bg: 'bg-codex-bg' },
  opencode: { label: 'OpenCode', mono: 'OC', fg: 'text-opencode', bg: 'bg-opencode-bg' },
} satisfies Record<string, Vendor>;

export type VendorKey = keyof typeof VENDORS;

export const VENDOR_KEYS = ['claude', 'codex', 'opencode'] as const satisfies readonly VendorKey[];

export function getSessionVendors(sessions: readonly Pick<Session, 'agent'>[]): VendorKey[] {
  return VENDOR_KEYS.filter((vendor) => sessions.some((session) => session.agent === vendor));
}

export const STATUSES = {
  busy: {
    label: 'Active',
    fg: 'text-success-fg',
    bg: 'bg-success-bg',
    dot: 'bg-success',
  },
  idle: {
    label: 'Idle',
    fg: 'text-info-fg',
    bg: 'bg-info-bg',
    dot: 'bg-info',
  },
  waiting: {
    label: 'Waiting',
    fg: 'text-warning-fg',
    bg: 'bg-warning-bg',
    dot: 'bg-warning',
  },
  inactive: {
    label: 'Inactive',
    fg: 'text-muted-foreground',
    bg: 'bg-muted',
    dot: 'bg-muted-foreground',
  },
  unknown: {
    label: 'Unknown',
    fg: 'text-muted-foreground',
    bg: 'bg-muted',
    dot: 'bg-muted-foreground',
  },
} satisfies Record<string, Status>;

export type StatusKey = keyof typeof STATUSES;

// Board columns render left to right in this order; status sorting uses the same priority.
export const STATUS_ORDER: readonly StatusKey[] = ['busy', 'waiting', 'idle', 'inactive', 'unknown'];

export type SubagentStatus = { label: string; fg: string; bg: string };

export const SUBAGENT_STATUSES = {
  running: { label: 'running', fg: 'text-warning-fg', bg: 'bg-warning-bg' },
  returned: { label: 'returned', fg: 'text-subagent', bg: 'bg-subagent-bg' },
  aborted: { label: 'aborted', fg: 'text-destructive', bg: 'bg-muted' },
} satisfies Record<Subagent['status'], SubagentStatus>;

const MODALITIES: Record<string, string> = {
  'cli': 'Interactive',
  'codex-tui': 'Interactive',
  'opencode-cli': 'CLI',
  'opencode-desktop': 'Desktop',
  'sdk-cli': 'Autonomous',
  'sdk-ts': 'Autonomous',
  'sdk-py': 'Autonomous',
  'codex_exec': 'Autonomous',
};

export function getModality(entrypoint: string | null): string {
  if (entrypoint == null) return '—';
  const trimmed = entrypoint.trim();
  const mapped = MODALITIES[trimmed] ?? MODALITIES[trimmed.toLowerCase()];
  if (mapped) return mapped;
  return trimmed
    .toLowerCase()
    .split(/[\s_-]+/)
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ');
}

export function getVendor(agent: string): Vendor {
  if (agent in VENDORS) return VENDORS[agent as VendorKey];
  return {
    label: agent,
    mono: '??',
    fg: 'text-muted-foreground',
    bg: 'bg-muted',
  };
}

export function getStatus(status: string | null): StatusKey {
  return status !== null && status in STATUSES ? (status as StatusKey) : 'inactive';
}

/** Board column and live status for a card; stale or unavailable liveness is unknown. */
export function boardStatusKey(session: Pick<Session, 'sourceId' | 'status' | 'displayStale'>): StatusKey {
  if (session.displayStale || (isLocalSource(session.sourceId) === false && session.status == null)) {
    return 'unknown';
  }
  return getStatus(session.status);
}

export function resumeDisabledHint(
  session: Pick<Session, 'sourceId' | 'agent' | 'status' | 'displayStale'>,
  remoteLaunchable = false,
  remoteLaunchHint?: string,
): string | undefined {
  if (
    boardStatusKey(session) === 'busy' &&
    (isLocalSession(session) ? session.agent === 'codex' : remoteLaunchable)
  ) {
    return 'This session is already active';
  }
  return !isLocalSession(session) && !remoteLaunchable ? remoteLaunchHint : undefined;
}

export function resumeDisabled(
  session: Pick<Session, 'sourceId' | 'displayStale' | 'launchable'>,
  disabledHint?: string,
): boolean {
  return isLocalSession(session)
    ? disabledHint != null
    : session.displayStale || session.launchable === false;
}

/** Badge label: live status, or "Last seen …" when the remote snapshot is stale/incomplete. */
export function displayStatusLabel(
  session: Pick<Session, 'status' | 'displayStale' | 'lastSeenStatus'> & { sourceId?: string },
): string {
  const remoteUnknown = session.sourceId != null && session.sourceId !== LOCAL_SOURCE_ID;
  if (remoteUnknown && session.status == null && session.lastSeenStatus == null) {
    return session.displayStale ? 'Liveness unknown · stale view' : 'Liveness unknown';
  }
  if (!session.displayStale) return STATUSES[getStatus(session.status)].label;
  const seen = getStatus(session.lastSeenStatus ?? session.status);
  const label = STATUSES[seen].label.toLowerCase();
  return `Last seen ${label}`;
}

export function sumTokens(
  tokens: Session['tokens'],
  key: Exclude<keyof Session['tokens'][string], 'cost'>,
): number {
  return Object.values(tokens).reduce((sum, modelTokens) => sum + modelTokens[key], 0);
}

export function getTotalTokens(tokens: Session['tokens']): number {
  return Object.values(tokens).reduce(
    (sum, modelTokens) =>
      sum +
      modelTokens.input_tokens +
      modelTokens.output_tokens +
      modelTokens.cache_creation_input_tokens +
      modelTokens.cache_creation_1h_input_tokens +
      modelTokens.cache_read_input_tokens,
    0,
  );
}
