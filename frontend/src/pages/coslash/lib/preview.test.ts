import { describe, expect, it } from 'vitest';
import {
  canonicalPayloadText,
  canonicalUploadBytes,
  formatCanonicalJson,
  frozenCostDisclosure,
  isSnapshotPreview,
  PREVIEW_PRIVACY_COPY,
  previewNotices,
  previewRequestPath,
  STRUCTURALLY_EXCLUDED,
  teamPreviewEnabled,
  type SnapshotPreview,
} from '@/pages/coslash/lib/preview';

function ready(snapshot: Record<string, unknown>): SnapshotPreview {
  const payload = JSON.stringify(snapshot);
  const bytes = new TextEncoder().encode(payload);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return {
    adapterVersion: 'snapshot-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    sourceRevision: 42,
    schemaVersion: 'session-snapshot/v1',
    mediaType: 'application/vnd.coslash.session-snapshot.v1+json',
    payloadBytes: bytes.byteLength,
    maxPayloadBytes: 262_144,
    canonicalPayloadBase64: btoa(binary),
    snapshot: snapshot as SnapshotPreview['snapshot'],
  };
}

describe('snapshot preview adapter', () => {
  it('returns the exact canonical bytes intended for upload', () => {
    const preview = ready({
      schemaVersion: 'session-snapshot/v1',
      contentHash: 'sha256:test',
      repository: { canonical: 'github.com/centauri-ai/coslash' },
    });
    expect(new TextDecoder().decode(canonicalUploadBytes(preview))).toBe(canonicalPayloadText(preview));
    expect(canonicalPayloadText(preview)).toContain('github.com/centauri-ai/coslash');
  });

  it('rejects any displayed snapshot field that differs from the canonical bytes', () => {
    const preview = ready({
      schemaVersion: 'session-snapshot/v1',
      contentHash: 'sha256:test',
      repository: { canonical: 'github.com/centauri-ai/coslash' },
    });
    preview.snapshot = {
      ...preview.snapshot,
      repository: { canonical: 'github.com/centauri-ai/other' },
    };
    expect(() => canonicalUploadBytes(preview)).toThrow('does not match');
  });

  it('blocks bytes for every non-ready state', () => {
    const preview: SnapshotPreview = {
      adapterVersion: 'snapshot-preview/v1',
      state: 'oversized',
      approvalAllowed: false,
      sourceRevision: 42,
      maxPayloadBytes: 262_144,
      problem: { code: 'aggregate_size_exceeded', message: 'Too large.', action: 'Reduce evidence.' },
    };
    expect(() => canonicalUploadBytes(preview)).toThrow('not approvable');
  });

  it('formats canonical JSON without changing string content', () => {
    const canonical = '{"summary":"comma, brace } and escaped \\\"quote\\\"","count":9007199254740993}';
    const formatted = formatCanonicalJson(canonical);
    expect(formatted).toContain('"comma, brace } and escaped \\\"quote\\\""');
    expect(formatted).toContain('9007199254740993');
  });

  it('exposes redaction, truncation, revision, and frozen-cost semantics', () => {
    const preview = ready({
      schemaVersion: 'session-snapshot/v1',
      contentHash: 'sha256:test',
      redactions: [{ path: '/session/firstPrompt', reason: 'credential_pattern' }],
      truncation: [{ path: '/session/firstPrompt', reason: 'text_budget' }],
      session: { usage: { estimatedCostMicroUsd: 0, unpricedModels: ['unknown'] } },
    });
    expect(previewNotices(preview)).toEqual([
      { kind: 'redaction', path: '/session/firstPrompt', reason: 'credential_pattern' },
      { kind: 'truncation', path: '/session/firstPrompt', reason: 'text_budget' },
    ]);
    expect(frozenCostDisclosure(preview)).toBe('$0.00 frozen priced estimate; 1 unpriced model excluded');
    expect(previewRequestPath('session/id', 42)).toBe('/api/share-preview?id=session%2Fid&revision=42');
  });

  it('distinguishes a true zero from unpriced models', () => {
    const preview = ready({
      schemaVersion: 'session-snapshot/v1',
      contentHash: 'sha256:test',
      session: { usage: { estimatedCostMicroUsd: 0, unpricedModels: [] } },
    });
    expect(frozenCostDisclosure(preview)).toBe('$0.00 frozen estimate; all models priced');
  });

  it('enables the Team preview harness only through its explicit URL flag', () => {
    expect(teamPreviewEnabled('?team-preview=1')).toBe(true);
    expect(teamPreviewEnabled('?team-preview=0')).toBe(false);
    expect(teamPreviewEnabled('?other=1')).toBe(false);
  });

  it('validates preview responses and states the credential limit precisely', () => {
    expect(isSnapshotPreview(null)).toBe(false);
    expect(isSnapshotPreview(ready({ schemaVersion: 'session-snapshot/v1' }))).toBe(true);
    expect(PREVIEW_PRIVACY_COPY).toContain('known credential patterns');
    expect(STRUCTURALLY_EXCLUDED.join(' ')).not.toContain('Credentials');
  });
});
