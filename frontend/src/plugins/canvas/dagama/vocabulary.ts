// The verified DaGama launch vocabularies and the validators that gate them.
//
// This module is a deliberate mirror of the backend's
// `collector/internal/plugins/canvas/dagama/vocabulary.go`. The server validates
// again — the client copy exists so the configuration UI can only *offer* values
// the server will accept, which turns a rejected save into an impossible one.
// Keeping it import-free keeps it usable from a policy check, a normalizer, and
// a test without dragging React or the API client along.
//
// Every value is an allowlist, not a suggestion list, because neither CLI fails
// fast on a bad one: Claude accepts an unknown --model and only errors at the
// API call, and an unknown --effort is a warning that silently uses the default.

export type DaGamaVendor = 'claude' | 'codex';

// The pipeline. The shape is the product, so it is not data: this array is both
// the membership rule and the order.
export const DAGAMA_COMPONENT_IDS = ['intake', 'plan', 'build', 'verify', 'review', 'publish'] as const;

export type DaGamaComponentId = (typeof DAGAMA_COMPONENT_IDS)[number];

// The components that run a model. The rest are deterministic: Intake renders a
// template, Verify runs argv, Publish drives git and gh.
export const DAGAMA_SEAT_COMPONENT_IDS = ['plan', 'build', 'review'] as const;

export type DaGamaSeatComponentId = (typeof DAGAMA_SEAT_COMPONENT_IDS)[number];

export function hasSeat(id: DaGamaComponentId): id is DaGamaSeatComponentId {
  return (DAGAMA_SEAT_COMPONENT_IDS as readonly string[]).includes(id);
}

export function isDaGamaComponentId(value: unknown): value is DaGamaComponentId {
  return typeof value === 'string' && (DAGAMA_COMPONENT_IDS as readonly string[]).includes(value);
}

export function isDaGamaSeatComponentId(value: unknown): value is DaGamaSeatComponentId {
  return typeof value === 'string' && (DAGAMA_SEAT_COMPONENT_IDS as readonly string[]).includes(value);
}

export const DAGAMA_CLAUDE_MODELS = ['opus', 'sonnet', 'haiku', 'fable'] as const;
export const DAGAMA_CLAUDE_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const;
// A deliberate SUBSET of Claude's --permission-mode choices. The CLI also accepts
// manual, plan, auto, and dontAsk; a board may only select a value that can
// actually finish an unattended turn that must write an artifact. `manual`
// prompts, so it hangs. `plan` cannot write files, so every component fails
// missing_output. `auto` and `dontAsk` have undocumented semantics.
//
// bypassPermissions is offered but is the loosest setting, and Claude has no
// sandbox, so it grants a full shell as the developer. The UI labels it as such.
export const DAGAMA_CLAUDE_PERMISSIONS = ['acceptEdits', 'bypassPermissions'] as const;

export const DAGAMA_CODEX_MODELS = ['gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna'] as const;
export const DAGAMA_CODEX_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max', 'ultra'] as const;
// danger-full-access is deliberately absent. workspace-write is the one
// OS-enforced boundary DaGama has, and with the run root being a clone the agent
// can still do everything legitimate inside it, so letting a board switch it off
// would discard the only real containment for no capability gained.
export const DAGAMA_CODEX_SANDBOXES = ['read-only', 'workspace-write'] as const;

export function modelsFor(vendor: DaGamaVendor): readonly string[] {
  return vendor === 'codex' ? DAGAMA_CODEX_MODELS : DAGAMA_CLAUDE_MODELS;
}

export function effortsFor(vendor: DaGamaVendor, model: string): readonly string[] {
  if (vendor === 'claude') return DAGAMA_CLAUDE_EFFORTS;
  // `ultra` is only offered by the models that support it, so a board can never
  // carry an effort the chosen model would silently ignore.
  return model === 'gpt-5.6-sol' || model === 'gpt-5.6-terra'
    ? DAGAMA_CODEX_EFFORTS
    : DAGAMA_CODEX_EFFORTS.filter((effort) => effort !== 'ultra');
}

export function permissionsFor(vendor: DaGamaVendor): readonly string[] {
  return vendor === 'codex' ? DAGAMA_CODEX_SANDBOXES : DAGAMA_CLAUDE_PERMISSIONS;
}

// Verify check bounds. These match the backend's MaxChecks / MaxArgvTokens /
// MaxArgvTokenChars exactly.
export const DAGAMA_MAX_CHECKS = 12;
export const DAGAMA_MAX_ARGV_TOKENS = 40;
export const DAGAMA_MAX_ARGV_TOKEN_LENGTH = 400;

// Programs a Verify check may run.
//
// Checks are exec'd as argv with no shell, so metacharacters are inert — but
// that protection is worthless if argv[0] is itself a shell. A board is a JSON
// file that can be committed, shared, or arrive in a pull request, so
// ["sh","-c","curl …|sh"] would make opening someone's board an execution
// vector. Notably absent: sh, bash, zsh, env, xargs, eval, and npx — npx
// resolves and executes packages from the network, which is the same hole
// wearing a hat.
export const DAGAMA_CHECK_COMMANDS = [
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

// Only ever shown to a human and used as a log filename stem, so it is
// restricted to characters that are safe in both roles.
const CHECK_NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$/;

// Git allows most bytes in a ref, but a base branch also reaches argv and a pull
// request query. A leading dash could be read as a flag and `..` is a traversal.
const BASE_BRANCH_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/;

// Board and project identifiers become path components under the private
// workflow root, so they are bounded the same way the backend bounds them.
const IDENTIFIER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

const RUN_ID_PATTERN = /^run-[0-9]{8}t[0-9]{6}-[0-9a-f]{8}$/;

export function validBaseBranch(value: string): boolean {
  return BASE_BRANCH_PATTERN.test(value) && !value.includes('..');
}

export function validCheckName(value: string): boolean {
  return CHECK_NAME_PATTERN.test(value);
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

// argv tokens are exec'd directly, so shell metacharacters are harmless. Control
// characters are not: they can truncate or confuse the exec boundary and the logs.
export function validArgvToken(value: unknown): value is string {
  if (typeof value !== 'string') return false;
  if (value === '' || value.length > DAGAMA_MAX_ARGV_TOKEN_LENGTH) return false;
  if (value.trim() !== value) return false;
  for (const character of value) {
    const code = character.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f) return false;
  }
  return true;
}

export function validCheckCommand(value: unknown): boolean {
  return typeof value === 'string' && (DAGAMA_CHECK_COMMANDS as readonly string[]).includes(value);
}
