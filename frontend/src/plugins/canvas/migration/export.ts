// The browser half of the legacy migration.
//
// `localStorage` is readable only by the page that owns the origin, so the
// collector cannot reach legacy browser state on its own. This module produces
// an export bundle the collector then imports; it is the only part of the
// migration that must run in a browser.
//
// Three rules shape it:
//
//   Values are carried VERBATIM, as the strings localStorage held. Reparsing
//   here would silently repair malformed JSON, and the collector would then
//   checksum a value the operator never had.
//
//   Nothing outside the allowlist is exported. Refused and unrecognized keys
//   are reported BY NAME ONLY, so the operator can see what was left behind
//   without the bundle carrying the contents.
//
//   The bundle is a description, not an instruction. It records what was found;
//   the collector decides what to do with it and refuses anything it does not
//   recognize.

import {
  keySuffix,
  matchAllowedKey,
  refusalReason,
  type MigrationKind,
} from '@/plugins/canvas/migration/keys';

export const LEGACY_EXPORT_SCHEMA_VERSION = 1;

/** One exported legacy key. `value` is the stored string, unmodified. */
export type LegacyExportRecord = {
  key: string;
  kind: MigrationKind;
  /** The variable part of a prefixed key: a session id, or a project id. */
  suffix: string;
  purpose: string;
  value: string;
  bytes: number;
};

/** A key deliberately left behind, named so the operator can see the gap. */
export type LegacySkippedKey = { key: string; reason: string };

export type LegacyExportBundle = {
  schemaVersion: number;
  source: 'fleetlog-canvas';
  /** ISO-8601. Supplied by the caller so the bundle is reproducible in tests. */
  exportedAt: string;
  records: LegacyExportRecord[];
  /** Keys refused by name, with the recorded reason. */
  refused: LegacySkippedKey[];
  /** Keys this build does not recognize at all. Names only. */
  unrecognized: string[];
  /** True when the cap below truncated the bundle. */
  truncated: boolean;
};

/** A minimal read-only view of `localStorage`, so tests need no DOM. */
export type LegacyStorage = {
  readonly length: number;
  key(index: number): string | null;
  getItem(key: string): string | null;
};

export type ExportOptions = {
  storage?: LegacyStorage;
  now?: () => Date;
  /** Bounds one exported value. A larger one is refused, not truncated. */
  maxValueBytes?: number;
  /** Bounds the whole bundle so an export cannot exhaust memory. */
  maxRecords?: number;
};

const DEFAULT_MAX_VALUE_BYTES = 1 << 20;
const DEFAULT_MAX_RECORDS = 2048;

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

/**
 * Read the allowlisted legacy keys out of browser storage.
 *
 * Only keys present in storage are reported: a legacy install that never used
 * DaGama produces a bundle with no DaGama records rather than empty ones.
 */
export function exportLegacyBrowserState(options: ExportOptions = {}): LegacyExportBundle {
  const storage = options.storage ?? (typeof localStorage === 'undefined' ? null : localStorage);
  const now = options.now ?? (() => new Date());
  const maxValueBytes = options.maxValueBytes ?? DEFAULT_MAX_VALUE_BYTES;
  const maxRecords = options.maxRecords ?? DEFAULT_MAX_RECORDS;

  const bundle: LegacyExportBundle = {
    schemaVersion: LEGACY_EXPORT_SCHEMA_VERSION,
    source: 'fleetlog-canvas',
    exportedAt: now().toISOString(),
    records: [],
    refused: [],
    unrecognized: [],
    truncated: false,
  };
  if (storage === null) return bundle;

  // Snapshot the key list before reading. Iterating an index while another tab
  // writes would skip entries, and a migration that silently skips is worse
  // than one that reports nothing.
  const keys: string[] = [];
  for (let index = 0; index < storage.length; index += 1) {
    const key = storage.key(index);
    if (key !== null) keys.push(key);
  }
  keys.sort();

  for (const key of keys) {
    const allowed = matchAllowedKey(key);
    if (allowed === null) {
      const reason = refusalReason(key);
      if (reason !== '') bundle.refused.push({ key, reason });
      // A key belonging to another app is not this migration's business, and
      // naming it in the bundle would be reporting on unrelated software.
      else if (key.startsWith('fleetlog.')) bundle.unrecognized.push(key);
      continue;
    }

    const value = storage.getItem(key);
    if (value === null) continue;
    const bytes = byteLength(value);
    if (bytes > maxValueBytes) {
      // Truncating would produce a value that parses to something the operator
      // never saved. Refusing keeps the legacy copy as the only source.
      bundle.refused.push({
        key,
        reason: `The stored value is ${bytes} bytes, over the ${maxValueBytes}-byte export limit. It was left in place rather than truncated.`,
      });
      continue;
    }
    if (bundle.records.length >= maxRecords) {
      bundle.truncated = true;
      break;
    }
    bundle.records.push({
      key,
      kind: allowed.kind,
      suffix: keySuffix(allowed, key),
      purpose: allowed.purpose,
      value,
      bytes,
    });
  }

  return bundle;
}

/**
 * Serialize a bundle for upload.
 *
 * Pretty-printed on purpose: an operator asked to hand a file to a migration is
 * entitled to read it first and see exactly what it carries.
 */
export function serializeLegacyExport(bundle: LegacyExportBundle): string {
  return `${JSON.stringify(bundle, null, 2)}\n`;
}
