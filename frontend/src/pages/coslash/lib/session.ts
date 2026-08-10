export type ModelTokens = {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_creation_1h_input_tokens: number;
  cache_read_input_tokens: number;
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

export type Session = {
  agent: string;
  id: string;
  name: string | null;
  summary: string | null;
  status: string | null;
  cwd: string;
  branch: string | null;
  repo: string | null;
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
  logPath: string;
  model: string | null;
  contextTokens: number | null;
  contextWindow: number | null;
  turns: number;
  toolUses: number;
  errors: number;
  compactions: number;
  firstPrompt: string | null;
  commandCount: number;
  commands: string[];
  commits: string[];
  prs: number;
  todos: { text: string; done: boolean }[];
  digest: DigestEntry[];
  fileEdits: FileEdit[];
  git: GitDrift | null;
  lastEditAt: number | null;
};

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
} satisfies Record<string, Vendor>;

export type VendorKey = keyof typeof VENDORS;

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
} satisfies Record<string, Status>;

export type StatusKey = keyof typeof STATUSES;

export type SubagentStatus = { label: string; fg: string; bg: string };

export const SUBAGENT_STATUSES = {
  running: { label: 'running', fg: 'text-warning-fg', bg: 'bg-warning-bg' },
  returned: { label: 'returned', fg: 'text-subagent', bg: 'bg-subagent-bg' },
  aborted: { label: 'aborted', fg: 'text-destructive', bg: 'bg-muted' },
} satisfies Record<Subagent['status'], SubagentStatus>;

const MODALITIES: Record<string, string> = {
  'cli': 'Interactive',
  'codex-tui': 'Interactive',
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

export function sumTokens(tokens: Session['tokens'], key: keyof Session['tokens'][string]): number {
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
