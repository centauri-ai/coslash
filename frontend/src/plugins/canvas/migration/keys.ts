// The legacy browser-state allowlist.
//
// Legacy Canvas kept its per-user state in `localStorage`. coSlash keeps it on
// the server, so the one thing the browser can still do that the collector
// cannot is READ that storage — which is why the export half of this migration
// lives in the frontend at all.
//
// This module is the allowlist, and it is deliberately a list rather than a
// pattern. A prefix match on `fleetlog.` would have swept up the Azure Foundry
// credentials the legacy app stored under `fleetlog.llmConfig`, and a migration
// that copies a key into server-backed storage is a migration that copies it
// into a file, a backup, and every diagnostic bundle taken afterwards. Every key
// below was read from the legacy sources at
// c13a3ef01438193dcdcd2e387300e69ae3c27437; anything not named here is not
// exported, including a key added after this list was written.

/** What a migrated key becomes on the server. */
export type MigrationKind =
  // Per-session Canvas workspace state: layout, checkpoints, pins.
  | 'workspace'
  // A per-product preference the operator would notice losing.
  | 'preference'
  // An unsaved board the operator may not have realized was only local.
  | 'draft';

export type AllowedKey = {
  /** The exact legacy key, or the constant prefix of a family of keys. */
  key: string;
  /** True when `key` is a prefix and the remainder is a variable id. */
  prefix: boolean;
  kind: MigrationKind;
  /** Why this is worth carrying across. Recorded in the journal verbatim. */
  purpose: string;
};

/**
 * Legacy keys this migration exports.
 *
 * The Columbus keys are deliberately absent: Columbus is not one of the three
 * products being ported, so its state has no destination here and exporting it
 * would produce records nothing can read.
 */
export const ALLOWED_LEGACY_KEYS: readonly AllowedKey[] = [
  {
    key: 'fleetlog.canvasWorkspace.v1:',
    prefix: true,
    kind: 'workspace',
    purpose: 'Session Canvas layout, checkpoints, and pins for one session',
  },
  {
    key: 'fleetlog.dagamaProject.v1',
    prefix: false,
    kind: 'preference',
    purpose: 'The DaGama project last opened',
  },
  {
    key: 'fleetlog.dagamaBoardId.v1.',
    prefix: true,
    kind: 'preference',
    purpose: 'The DaGama workflow last opened in one project',
  },
  {
    key: 'fleetlog.dagamaRunId.v1.',
    prefix: true,
    kind: 'preference',
    purpose: 'The DaGama run last watched in one project',
  },
  {
    key: 'fleetlog.dagamaDraft.v1',
    prefix: false,
    kind: 'draft',
    purpose: 'An unsaved DaGama workflow that exists only in this browser',
  },
  {
    key: 'fleetlog.dagamaDraftMetadata.v1',
    prefix: false,
    kind: 'draft',
    purpose: 'Which stored workflow the unsaved DaGama draft was derived from',
  },
  {
    key: 'fleetlog.atlasProject.v1',
    prefix: false,
    kind: 'preference',
    purpose: 'The Atlas project last opened',
  },
  {
    key: 'fleetlog.atlasBoardId.v1.',
    prefix: true,
    kind: 'preference',
    purpose: 'The Atlas workflow last opened in one project',
  },
  {
    key: 'fleetlog.atlasRunId.v1.',
    prefix: true,
    kind: 'preference',
    purpose: 'The Atlas run last watched in one project',
  },
  {
    key: 'fleetlog.atlasDraft.v1',
    prefix: false,
    kind: 'draft',
    purpose: 'An unsaved Atlas workflow that exists only in this browser',
  },
  {
    key: 'fleetlog.atlasDraftMetadata.v1',
    prefix: false,
    kind: 'draft',
    purpose: 'Which stored workflow the unsaved Atlas draft was derived from',
  },
  {
    key: 'fleetlog.canvasSessionId',
    prefix: false,
    kind: 'preference',
    purpose: 'The session Canvas was last opened on',
  },
];

/**
 * Legacy keys this migration refuses, and why.
 *
 * Naming a refusal is not decoration. `fleetlog.llmConfig` holds an Azure
 * Foundry `apiKey` in cleartext; without an explicit entry the next person to
 * extend the allowlist has nothing telling them that. The exporter asserts
 * against this list, so a key that appears in both is a test failure rather
 * than a silent export.
 */
export const REFUSED_LEGACY_KEYS: readonly { key: string; prefix: boolean; reason: string }[] = [
  {
    key: 'fleetlog.llmConfig',
    prefix: false,
    reason: 'Holds an Azure Foundry endpoint and apiKey in cleartext. Credentials are never migrated.',
  },
  {
    key: 'fleetlog.terminalConfig',
    prefix: false,
    reason:
      'Legacy terminal preferences describe the ttyd embed, which coSlash replaced with a native PTY. There is nothing to carry across.',
  },
  {
    key: 'fleetlog.turnAnalysis.v1',
    prefix: true,
    reason:
      'A cache of model output about session turns. It is derived, potentially large, and regenerated on demand.',
  },
  {
    key: 'fleetlog.digestPrompt',
    prefix: false,
    reason: 'A digest system prompt belonging to the Log product, not to Canvas.',
  },
  {
    key: 'fleetlog.dailyDigestWindow',
    prefix: false,
    reason: 'A Log preference, not Canvas state.',
  },
  {
    key: 'fleetlog.columbus',
    prefix: true,
    reason:
      'Columbus is not one of the ported products. Its workspace, archives, boards, and drafts have no destination in coSlash.',
  },
];

/** The allowlist entry a legacy key matches, or null when it matches none. */
export function matchAllowedKey(key: string): AllowedKey | null {
  for (const allowed of ALLOWED_LEGACY_KEYS) {
    if (allowed.prefix ? key.startsWith(allowed.key) && key.length > allowed.key.length : key === allowed.key) {
      return allowed;
    }
  }
  return null;
}

/** The refusal reason for a key, or an empty string when none is recorded. */
export function refusalReason(key: string): string {
  for (const refused of REFUSED_LEGACY_KEYS) {
    if (refused.prefix ? key.startsWith(refused.key) : key === refused.key) return refused.reason;
  }
  return '';
}

/** The variable remainder of a prefixed key — a session id, or a project id. */
export function keySuffix(allowed: AllowedKey, key: string): string {
  return allowed.prefix ? key.slice(allowed.key.length) : '';
}
