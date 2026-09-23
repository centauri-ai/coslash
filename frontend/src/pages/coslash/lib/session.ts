import { promptCacheTiming } from '@/pages/coslash/lib/time';

type ModelTokens = {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_creation_1h_input_tokens: number;
  cache_read_input_tokens: number;
  cost?: number;
};

type SessionSynthesis = {
  goals: string[];
  outcome: string;
  keyDecisions: string[];
  nextStep: string;
};

export type SubagentCommand = { label: string; command: string };

export type Subagent = {
  id: string;
  parentId?: string;
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
  cost: number | null;
};

export function subagentParentName(
  subagent: Pick<Subagent, 'parentId'>,
  subagents: readonly Pick<Subagent, 'id' | 'name'>[],
  rootName: string | null,
): string | null {
  return subagents.find((candidate) => candidate.id === subagent.parentId)?.name ?? rootName;
}

export const LOCAL_SOURCE_ID = 'local';
const LOCAL_SOURCE_LABEL = 'Local Mac';
const SSH_SOURCE_LABEL = 'SSH workspace';

export type SourceClass = 'local' | 'ssh_workspace';
type SessionCompletion = 'complete' | 'running' | 'incomplete';
type SessionPrivacy = 'shareable' | 'private';
export type ShareEligibility =
  'eligible' | 'private' | 'running' | 'failed' | 'incomplete' | 'stale' | 'offline' | 'deleted';

export type Session = {
  sourceId: string;
  sourceLabel: string;
  /** Stable across a remote alias rename. Never contains host or path data. */
  sourceClass?: SourceClass;
  logicalSessionId?: string;
  revision?: number;
  /** Revision guard used by detail and change-body reads. */
  detailRevision: string;
  /** Immutable full-record revision when this row supports full-v2 sharing. */
  fullRevision?: string;
  completion?: SessionCompletion;
  privacy?: SessionPrivacy;
  shareEligibility?: ShareEligibility;
  eligibleForAggregates: boolean;
  displayStale: boolean;
  launchable?: boolean;
  launchBlockReason?: 'missing_details' | 'oversized';
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
  cost: number | null;
  unpricedModels: string[];
  subagents: Subagent[];
  mtime: number;
  entrypoint: string | null;
  reviewPending?: boolean;
  reviewError?: string;
  synthesis: SessionSynthesis | null;
  synthesisPending: boolean;
  synthesisError?: string;
  declaredGoal: string | null;
  model: string | null;
  observedModels?: string[];
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

export function isLocalSource(sourceId: string): boolean {
  return sourceId === LOCAL_SOURCE_ID;
}

export function isLocalSession(session: Pick<Session, 'sourceId'>): boolean {
  return isLocalSource(session.sourceId);
}

export function sessionLogicalId(
  session: Pick<Session, 'sourceId' | 'agent' | 'id' | 'logicalSessionId'>,
): string {
  return session.logicalSessionId ?? `${session.sourceId}:${session.agent}:${session.id}`;
}

export function sessionRevision(session: Pick<Session, 'mtime' | 'revision'>): number {
  return session.revision ?? session.mtime;
}

export function sessionShareEligibility(
  session: Pick<
    Session,
    'shareEligibility' | 'repoLocalOnly' | 'status' | 'displayStale' | 'eligibleForAggregates'
  >,
): ShareEligibility {
  if (session.shareEligibility != null) return session.shareEligibility;
  if (session.repoLocalOnly) return 'private';
  if (session.displayStale) return 'stale';
  if (!session.eligibleForAggregates) return 'incomplete';
  if (session.status != null) return 'running';
  return 'eligible';
}

export function isEligibleForSharing(
  session: Pick<
    Session,
    'shareEligibility' | 'repoLocalOnly' | 'status' | 'displayStale' | 'eligibleForAggregates'
  >,
): boolean {
  return sessionShareEligibility(session) === 'eligible';
}

export function sessionsForAggregates<T extends Pick<Session, 'eligibleForAggregates'>>(
  sessions: readonly T[],
): T[] {
  return sessions.filter((session) => session.eligibleForAggregates);
}

export function sumKnown(values: (number | null)[]): number | null {
  if (values.length === 0) return null;
  let total = 0;
  for (const value of values) {
    if (value == null) return null;
    total += value;
  }
  return total;
}

/** Missing or blank remote env facts render as an em dash, never `undefined`. */
export function environmentFact(value: string | null | undefined): string {
  const trimmed = value?.trim();
  return trimmed ? trimmed : '—';
}

export function sessionLocationFact(session: Pick<Session, 'repo' | 'repoLocalOnly' | 'cwd'>): string {
  return environmentFact(
    session.repoLocalOnly ? session.cwd.trim() || session.repo : session.repo?.trim() || session.cwd,
  );
}

export function withLocalSourceDefaults<T extends { agent: string; id: string }>(
  session: T,
): T &
  Pick<
    Session,
    | 'sourceId'
    | 'sourceLabel'
    | 'sourceClass'
    | 'logicalSessionId'
    | 'revision'
    | 'detailRevision'
    | 'completion'
    | 'privacy'
    | 'shareEligibility'
    | 'eligibleForAggregates'
    | 'displayStale'
  > {
  const record = session as T & Partial<Session>;
  const sourceId = record.sourceId ?? LOCAL_SOURCE_ID;
  const sourceClass = record.sourceClass ?? (sourceId === LOCAL_SOURCE_ID ? 'local' : 'ssh_workspace');
  const sourceLabel =
    sourceClass === 'ssh_workspace' ? SSH_SOURCE_LABEL : (record.sourceLabel ?? LOCAL_SOURCE_LABEL);
  const eligibleForAggregates = record.eligibleForAggregates ?? true;
  const displayStale = record.displayStale ?? false;
  const shareEligibility = sessionShareEligibility({
    shareEligibility: record.shareEligibility,
    repoLocalOnly: record.repoLocalOnly ?? false,
    status: record.status ?? null,
    displayStale,
    eligibleForAggregates,
  });
  return {
    ...session,
    sourceId,
    sourceLabel,
    sourceClass,
    logicalSessionId: record.logicalSessionId ?? `${sourceId}:${session.agent}:${session.id}`,
    revision: record.revision ?? record.mtime ?? 0,
    detailRevision: record.detailRevision ?? String(record.revision ?? record.mtime ?? 0),
    completion:
      record.completion ??
      (shareEligibility === 'running' ? 'running' : eligibleForAggregates ? 'complete' : 'incomplete'),
    privacy: record.privacy ?? (record.repoLocalOnly ? 'private' : 'shareable'),
    shareEligibility,
    eligibleForAggregates,
    displayStale,
  };
}

type FileEdit = {
  path: string;
  adds: number;
  dels: number;
  edits: number;
  isNew: boolean;
  changeIds?: string[];
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

type GoalSource = 'declared' | 'inferred' | 'floor';

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

function firstSentence(text: string | null | undefined): string | null {
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
  cursor: { label: 'Cursor', mono: 'CU', fg: 'text-cursor', bg: 'bg-cursor-bg' },
} satisfies Record<string, Vendor>;

export type VendorKey = keyof typeof VENDORS;

const VENDOR_KEYS = ['claude', 'codex', 'opencode', 'cursor'] as const satisfies readonly VendorKey[];

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
    fg: 'text-coslash-muted',
    bg: 'bg-coslash-soft',
    dot: 'bg-coslash-muted',
  },
} satisfies Record<string, Status>;

export type StatusKey = keyof typeof STATUSES;

// Board columns render left to right in this order; status sorting uses the same priority.
export const STATUS_ORDER: readonly StatusKey[] = ['busy', 'waiting', 'idle', 'inactive'];

type SubagentStatus = { label: string; fg: string; bg: string };

export const SUBAGENT_STATUSES = {
  running: { label: 'running', fg: 'text-warning-fg', bg: 'bg-warning-bg' },
  returned: { label: 'returned', fg: 'text-subagent', bg: 'bg-subagent-bg' },
  aborted: { label: 'aborted', fg: 'text-danger-fg', bg: 'bg-coslash-soft' },
} satisfies Record<Subagent['status'], SubagentStatus>;

const MODALITIES: Record<string, string> = {
  'cli': 'Interactive',
  'codex-tui': 'Interactive',
  'opencode-cli': 'CLI',
  'opencode-desktop': 'Desktop',
  'cursor-cli': 'CLI',
  'cursor-ide': 'IDE',
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
    fg: 'text-coslash-muted',
    bg: 'bg-coslash-soft',
  };
}

function getStatus(status: string | null): StatusKey {
  return status !== null && status in STATUSES ? (status as StatusKey) : 'inactive';
}

/** A missing status or a stale snapshot is Inactive; the badge still says what was last seen. */
export function boardStatusKey(session: Pick<Session, 'status' | 'displayStale'>): StatusKey {
  if (session.displayStale) return 'inactive';
  return getStatus(session.status);
}

export function resumeDisabledHint(
  session: Pick<Session, 'sourceId' | 'agent' | 'status' | 'displayStale'> &
    Partial<Pick<Session, 'entrypoint' | 'cwd'>>,
  remoteLaunchable = false,
  remoteLaunchHint?: string,
): string | undefined {
  if (isLocalSession(session) && session.agent === 'cursor') {
    if (!session.cwd) return 'This session has no usable working directory';
    if (session.entrypoint !== 'cursor-cli' && session.entrypoint !== 'cursor-ide') {
      return 'Launch is not available for this Cursor session';
    }
    if (
      session.entrypoint === 'cursor-cli' &&
      (boardStatusKey(session) === 'busy' || boardStatusKey(session) === 'idle')
    ) {
      return 'This session is already active';
    }
    return undefined;
  }
  if (
    (boardStatusKey(session) === 'busy' ||
      (!isLocalSession(session) && boardStatusKey(session) === 'idle')) &&
    (isLocalSession(session) ? session.agent === 'codex' : remoteLaunchable)
  ) {
    return 'This session is already active';
  }
  return !isLocalSession(session) && !remoteLaunchable ? remoteLaunchHint : undefined;
}

export function canResumeSession(
  session: Pick<Session, 'sourceId' | 'agent'> & Partial<Pick<Session, 'entrypoint'>>,
): boolean {
  if (!isLocalSession(session) || session.agent !== 'cursor') return true;
  return session.entrypoint === 'cursor-cli';
}

export function freshLaunchDisabledHint(
  session: Pick<Session, 'sourceId' | 'agent'> & Partial<Pick<Session, 'entrypoint' | 'cwd'>>,
): string | undefined {
  if (!isLocalSession(session) || session.agent !== 'cursor') return undefined;
  if (!session.cwd) return 'This session has no usable working directory';
  return session.entrypoint === 'cursor-cli' || session.entrypoint === 'cursor-ide'
    ? undefined
    : 'Launch is not available for this Cursor session';
}

export function resumeDisabled(
  session: Pick<Session, 'sourceId' | 'displayStale' | 'launchable'> & Partial<Pick<Session, 'status'>>,
  disabledHint?: string,
): boolean {
  return isLocalSession(session)
    ? disabledHint != null
    : session.status === 'busy' ||
        session.status === 'idle' ||
        session.displayStale ||
        session.launchable === false;
}

export function remoteLaunchDisabledHint(
  machineState: string | undefined,
  blockReason?: Session['launchBlockReason'],
): string {
  if (machineState == null || machineState === 'connecting') return 'Checking SSH liveness…';
  if (machineState !== 'ok' && machineState !== 'limited') return 'Remote is offline';
  if (blockReason === 'oversized') return 'Remote session details exceed the collection size limit';
  if (blockReason === 'missing_details') return 'Remote session is missing its working directory';
  return 'Waiting for remote session details';
}

/** Badge label: live status, or "Last seen …" when the remote snapshot is stale. */
export function displayStatusLabel(
  session: Pick<Session, 'status' | 'displayStale' | 'lastSeenStatus'>,
): string {
  if (!session.displayStale) return STATUSES[getStatus(session.status)].label;
  const seen = getStatus(session.lastSeenStatus ?? session.status);
  return `Last seen ${STATUSES[seen].label.toLowerCase()}`;
}

export function sumTokens(
  tokens: Session['tokens'],
  key: Exclude<keyof Session['tokens'][string], 'cost'>,
): number {
  return Object.values(tokens).reduce((sum, modelTokens) => sum + modelTokens[key], 0);
}

export function getTotalTokens(tokens: Session['tokens']): number | null {
  if (Object.keys(tokens).length === 0) return null;
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

export type SessionReadiness = {
  key: 'resume' | 'inspect' | 'fresh' | 'unavailable';
  label: 'Resume' | 'Inspect' | 'Start fresh' | 'Check host';
  detail: string;
  cacheWarm: boolean;
};

/** Median context growth per turn measured across the local session corpus. */
const TOKENS_PER_TURN = 18_400;
/** Agents compact near this share of the window, so room above it is not usable. */
const USABLE_WINDOW = 0.85;
const FRESH_TURNS = 2;
const RESUME_TURNS = 5;

/** Null when the session carries no measurable context, which both readings depend on. */
function contextStanding(
  session: Pick<Session, 'contextTokens' | 'contextWindow'>,
): { turns: number; usedPct: number } | null {
  const used = session.contextTokens;
  const window = session.contextWindow;
  if (used == null || window == null || window <= 0) return null;
  // An intrinsically small window should not look spent before the session starts.
  const tokensPerTurn = Math.min(TOKENS_PER_TURN, (window * USABLE_WINDOW) / RESUME_TURNS);
  return {
    turns: Math.max(0, Math.floor((window * USABLE_WINDOW - used) / tokensPerTurn)),
    usedPct: Math.round((used / window) * 100),
  };
}

export function sessionReadiness(
  session: Pick<Session, 'sourceId' | 'status' | 'contextTokens' | 'contextWindow' | 'compactions' | 'mtime'>,
  now = Date.now(),
): SessionReadiness {
  const unavailable: SessionReadiness = {
    key: 'unavailable',
    label: 'Check host',
    detail: 'Live context unavailable',
    cacheWarm: false,
  };
  const cacheWarm = promptCacheTiming(session.mtime, now).within1h;
  const standing = contextStanding(session);
  // A remote session has no live status until its host syncs, but its cached context still describes it.
  if (standing == null && !isLocalSession(session)) return unavailable;

  const signal = cacheWarm
    ? 'warm cache'
    : session.compactions >= 1
      ? `${session.compactions} ${session.compactions === 1 ? 'compaction' : 'compactions'}`
      : standing && `${standing.turns} ${standing.turns === 1 ? 'turn' : 'turns'} headroom`;
  const detail =
    [standing && `${standing.usedPct}% context`, signal].filter(Boolean).join(' · ') ||
    'Context estimate unavailable';

  if (session.compactions >= 2) return { key: 'fresh', label: 'Start fresh', detail, cacheWarm };
  if (standing == null) return { key: 'inspect', label: 'Inspect', detail, cacheWarm };
  if (standing.turns < FRESH_TURNS) {
    return { key: 'fresh', label: 'Start fresh', detail, cacheWarm };
  }
  if (standing.turns >= RESUME_TURNS) return { key: 'resume', label: 'Resume', detail, cacheWarm };
  return { key: 'inspect', label: 'Inspect', detail, cacheWarm };
}
