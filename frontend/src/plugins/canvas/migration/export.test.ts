import { describe, expect, it } from 'vitest';
import {
  exportLegacyBrowserState,
  serializeLegacyExport,
  type LegacyStorage,
} from '@/plugins/canvas/migration/export';

/** A `localStorage` stand-in, so these tests need no DOM. */
function fakeStorage(entries: Record<string, string>): LegacyStorage {
  const keys = Object.keys(entries);
  return {
    get length() {
      return keys.length;
    },
    key: (index) => keys[index] ?? null,
    getItem: (key) => entries[key] ?? null,
  };
}

const AT = () => new Date('2026-08-09T18:30:00Z');

const LEGACY = {
  'fleetlog.canvasWorkspace.v1:0f9a4d1e': '{"version":1,"layout":{},"checkpoints":[],"pinIds":[]}',
  'fleetlog.dagamaProject.v1': '/Users/example/code/demo',
  'fleetlog.dagamaDraft.v1': '{"kind":"dagama","schemaVersion":1}',
  'fleetlog.atlasBoardId.v1.demo': 'board-1',
  'fleetlog.llmConfig': '{"endpoint":"https://example.invalid","apiKey":"sk-live-SECRET"}',
  'fleetlog.columbusWorkspace.v1': '{"nodes":[]}',
  'fleetlog.somethingAddedLater': 'x',
  'unrelated.app.key': 'y',
};

describe('the legacy browser-state export', () => {
  it('carries the allowlisted keys and nothing else', () => {
    const bundle = exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT });
    expect(bundle.records.map((record) => record.key)).toEqual([
      'fleetlog.atlasBoardId.v1.demo',
      'fleetlog.canvasWorkspace.v1:0f9a4d1e',
      'fleetlog.dagamaDraft.v1',
      'fleetlog.dagamaProject.v1',
    ]);
  });

  it('never lets a credential reach the bundle, in any field', () => {
    // The strongest form of this assertion: the secret must not appear
    // anywhere in the serialized output, not merely outside `records`.
    const serialized = serializeLegacyExport(
      exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT }),
    );
    expect(serialized).not.toContain('sk-live-SECRET');
    expect(serialized).not.toContain('example.invalid');
  });

  it('names a refused key so the operator can see the gap', () => {
    const bundle = exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT });
    const refused = bundle.refused.find((entry) => entry.key === 'fleetlog.llmConfig');
    expect(refused?.reason).toContain('Credentials are never migrated');
    // Named, but its contents are not carried.
    expect(JSON.stringify(refused)).not.toContain('sk-live');
  });

  it('reports an unrecognized legacy key without exporting it', () => {
    const bundle = exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT });
    expect(bundle.unrecognized).toEqual(['fleetlog.somethingAddedLater']);
    expect(bundle.records.some((record) => record.key === 'fleetlog.somethingAddedLater')).toBe(false);
  });

  it('says nothing about another application’s keys', () => {
    // Reporting on unrelated software is not this migration's business.
    const bundle = exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT });
    expect(JSON.stringify(bundle)).not.toContain('unrelated.app.key');
  });

  it('carries the stored value verbatim, including malformed JSON', () => {
    // Reparsing here would repair the value silently, and the collector would
    // then checksum something the operator never had.
    const broken = '{"version":1,"layout":';
    const bundle = exportLegacyBrowserState({
      storage: fakeStorage({ 'fleetlog.canvasWorkspace.v1:abc': broken }),
      now: AT,
    });
    expect(bundle.records[0].value).toBe(broken);
  });

  it('refuses an oversized value rather than truncating it', () => {
    const huge = 'x'.repeat(2048);
    const bundle = exportLegacyBrowserState({
      storage: fakeStorage({ 'fleetlog.dagamaDraft.v1': huge }),
      now: AT,
      maxValueBytes: 1024,
    });
    expect(bundle.records).toHaveLength(0);
    expect(bundle.refused[0].reason).toContain('rather than truncated');
  });

  it('counts bytes rather than characters', () => {
    // A multi-byte value that fits by character count can still exceed a byte
    // limit, and the collector's own bound is in bytes.
    const bundle = exportLegacyBrowserState({
      storage: fakeStorage({ 'fleetlog.dagamaProject.v1': '“…”' }),
      now: AT,
    });
    expect(bundle.records[0].bytes).toBeGreaterThan(3);
  });

  it('reports truncation instead of quietly stopping', () => {
    const many: Record<string, string> = {};
    for (let index = 0; index < 10; index += 1) many[`fleetlog.atlasBoardId.v1.p${index}`] = 'b';
    const bundle = exportLegacyBrowserState({ storage: fakeStorage(many), now: AT, maxRecords: 4 });
    expect(bundle.records).toHaveLength(4);
    expect(bundle.truncated).toBe(true);
  });

  it('separates a prefixed key into its constant and variable parts', () => {
    const bundle = exportLegacyBrowserState({
      storage: fakeStorage({ 'fleetlog.canvasWorkspace.v1:0f9a4d1e': '{}' }),
      now: AT,
    });
    expect(bundle.records[0].suffix).toBe('0f9a4d1e');
    expect(bundle.records[0].kind).toBe('workspace');
  });

  it('produces an empty bundle where there is no storage at all', () => {
    const bundle = exportLegacyBrowserState({ storage: fakeStorage({}), now: AT });
    expect(bundle.records).toHaveLength(0);
    expect(bundle.truncated).toBe(false);
    expect(bundle.exportedAt).toBe('2026-08-09T18:30:00.000Z');
  });

  it('is stable across exports of the same storage', () => {
    // A rerun that reorders records would look like a change to every
    // downstream diff and to the collector's idempotency check.
    const first = serializeLegacyExport(exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT }));
    const second = serializeLegacyExport(exportLegacyBrowserState({ storage: fakeStorage(LEGACY), now: AT }));
    expect(first).toBe(second);
  });
});
