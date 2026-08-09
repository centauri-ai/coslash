// The verified Atlas launch vocabularies and the validators that gate them.
//
// A deliberate mirror of `collector/internal/plugins/canvas/atlas/vocabulary.go`.
// The server validates again; this copy exists so the editor can only ever
// offer a value the server will accept, which turns a rejected save into an
// impossible one. Import-free so a normalizer, a policy check, and a test can
// all use it without dragging React along.

export type AtlasVendor = 'claude' | 'codex';

/** The stages of the runnable pipeline. A freeform seat has no legacy role. */
export const ATLAS_COMPONENT_IDS = ['intake', 'plan', 'build', 'verify', 'review', 'publish'] as const;

export type AtlasComponentID = (typeof ATLAS_COMPONENT_IDS)[number];

/** The stages that run a model, and therefore the only ones with a committee. */
export const ATLAS_SEAT_COMPONENT_IDS = ['plan', 'build', 'review'] as const;

export type AtlasSeatComponentID = (typeof ATLAS_SEAT_COMPONENT_IDS)[number];

export function hasSeat(id: string): id is AtlasSeatComponentID {
  return (ATLAS_SEAT_COMPONENT_IDS as readonly string[]).includes(id);
}

export function isAtlasComponentID(value: unknown): value is AtlasComponentID {
  return typeof value === 'string' && (ATLAS_COMPONENT_IDS as readonly string[]).includes(value);
}

/** A committee member either writes the promoted artifact or only drafts. */
export const ATLAS_WORKER_ROLES = ['main', 'worker'] as const;
export type AtlasWorkerRole = (typeof ATLAS_WORKER_ROLES)[number];

/** Edges are typed: a trigger advances the graph, feedback returns to Build. */
export const ATLAS_EDGE_KINDS = ['trigger', 'feedback'] as const;
export type AtlasEdgeKind = (typeof ATLAS_EDGE_KINDS)[number];

export const ATLAS_TRIGGER_MODES = ['auto', 'manual'] as const;
export type AtlasTriggerMode = (typeof ATLAS_TRIGGER_MODES)[number];

export const ATLAS_CLAUDE_MODELS = ['opus', 'sonnet', 'haiku', 'fable'] as const;
export const ATLAS_CLAUDE_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const;
// A deliberate SUBSET of Claude's --permission-mode choices. `manual` prompts,
// so an unattended turn hangs; `plan` cannot write files, so every stage fails
// missing_output; `auto` and `dontAsk` have undocumented semantics.
// bypassPermissions is offered but grants a full shell as the developer, and
// the UI labels it as such.
export const ATLAS_CLAUDE_PERMISSIONS = ['acceptEdits', 'bypassPermissions'] as const;

export const ATLAS_CODEX_MODELS = ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'] as const;
export const ATLAS_CODEX_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max', 'ultra'] as const;
// danger-full-access is deliberately absent: workspace-write is the one
// OS-enforced boundary Atlas has, and the agent can still do everything
// legitimate inside its run root.
export const ATLAS_CODEX_SANDBOXES = ['read-only', 'workspace-write'] as const;

export function modelsFor(vendor: AtlasVendor): readonly string[] {
  return vendor === 'codex' ? ATLAS_CODEX_MODELS : ATLAS_CLAUDE_MODELS;
}

export function effortsFor(vendor: AtlasVendor, model: string): readonly string[] {
  if (vendor !== 'codex') return ATLAS_CLAUDE_EFFORTS;
  // `ultra` is offered only by the models that support it, so a board can never
  // carry an effort the chosen model would silently ignore.
  return model === 'gpt-5.6-sol' || model === 'gpt-5.6-terra'
    ? ATLAS_CODEX_EFFORTS
    : ATLAS_CODEX_EFFORTS.filter((effort) => effort !== 'ultra');
}

export function permissionsFor(vendor: AtlasVendor): readonly string[] {
  return vendor === 'codex' ? ATLAS_CODEX_SANDBOXES : ATLAS_CLAUDE_PERMISSIONS;
}

// Committee and feedback bounds, matching the backend exactly.
export const ATLAS_MAX_WORKERS = 5;
export const ATLAS_DEFAULT_FEEDBACK_MAX_ROUNDS = 1;
export const ATLAS_MAX_FEEDBACK_ROUNDS = 2;
export const ATLAS_MAX_CHECKS = 12;
export const ATLAS_MAX_ARGV_TOKENS = 40;
export const ATLAS_MAX_ARGV_TOKEN_LENGTH = 400;

/** Programs a verification check may run. Checks are exec'd without a shell. */
export const ATLAS_CHECK_COMMANDS = [
  'npm',
  'pnpm',
  'yarn',
  'bun',
  'node',
  'deno',
  'make',
  'just',
  'cargo',
  'go',
  'python3',
  'pytest',
  'ruff',
  'tsc',
  'vitest',
  'jest',
  'eslint',
  'prettier',
  'mvn',
  'gradle',
  'dotnet',
  'swift',
  'bundle',
  'rake',
  'rspec',
  'phpunit',
  'composer',
] as const;

const CHECK_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$/;
const BASE_BRANCH_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/;
// A graph id becomes a node id, a CSS class, and an edge endpoint, so it is
// bounded to what is safe in all three.
const GRAPH_ID_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
const IDENTIFIER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
const RUN_ID_PATTERN = /^run-[0-9]{8}t[0-9]{6}-[0-9a-f]{8}$/;
// A required output is a path relative to an attempt directory.
const REQUIRED_OUTPUT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(\.[A-Za-z0-9]{1,16})?$/;

export function validCheckName(value: string): boolean {
  return CHECK_NAME_PATTERN.test(value);
}

export function validBaseBranch(value: string): boolean {
  return BASE_BRANCH_PATTERN.test(value) && !value.includes('..');
}

export function validGraphId(value: string): boolean {
  return GRAPH_ID_PATTERN.test(value);
}

export function validBoardId(value: string): boolean {
  return IDENTIFIER_PATTERN.test(value);
}

export function validProjectId(value: string): boolean {
  return IDENTIFIER_PATTERN.test(value);
}

export function validRunId(value: string): boolean {
  return RUN_ID_PATTERN.test(value);
}

export function validRequiredOutput(value: string): boolean {
  return REQUIRED_OUTPUT_PATTERN.test(value) && !value.includes('..');
}

/** argv tokens are exec'd directly, so control characters are what matter. */
export function validArgvToken(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  if (value === '' || value.length > ATLAS_MAX_ARGV_TOKEN_LENGTH) return false;
  if (value.trim() !== value) return false;
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) return false;
  }
  return true;
}

export function validCheckCommand(value: unknown): boolean {
  return typeof value === 'string' && (ATLAS_CHECK_COMMANDS as readonly string[]).includes(value);
}
