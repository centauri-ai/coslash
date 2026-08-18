export type JsonValue = null | boolean | number | string | JsonValue[] | { [key: string]: JsonValue };

export type PreviewState = 'ready' | 'invalid' | 'unsupported_version' | 'stale_source' | 'oversized';

export type PreviewProblem = { code: string; message: string; action: string };

export type SnapshotPreview = {
  adapterVersion: 'snapshot-preview/v1';
  state: PreviewState;
  approvalAllowed: boolean;
  sourceRevision: number;
  schemaVersion?: 'session-snapshot/v1';
  mediaType?: 'application/vnd.coslash.session-snapshot.v1+json';
  payloadBytes?: number;
  maxPayloadBytes: number;
  canonicalPayloadBase64?: string;
  snapshot?: { [key: string]: JsonValue };
  problem?: PreviewProblem;
};

export const PREVIEW_PRIVACY_COPY =
  'When present, firstPrompt is shared up to 16 KiB after credential-pattern redaction. Prompt data can therefore be included; this preview shows the exact bounded value.';

export const STRUCTURALLY_EXCLUDED = [
  'Raw transcripts and assistant reasoning',
  'Tool output and file diffs',
  'Raw top-level and subagent commands',
  'Credentials, environment variables, and unresolved local paths',
] as const;

export function previewRequestPath(id: string, revision: number): string {
  return `/api/share-preview?${new URLSearchParams({ id, revision: String(revision) })}`;
}

export function teamPreviewEnabled(search: string): boolean {
  return new URLSearchParams(search).get('team-preview') === '1';
}

export function canonicalUploadBytes(preview: SnapshotPreview): Uint8Array {
  if (
    preview.state !== 'ready' ||
    !preview.approvalAllowed ||
    preview.canonicalPayloadBase64 == null ||
    preview.payloadBytes == null ||
    preview.snapshot == null
  ) {
    throw new Error(`Preview state ${preview.state} is not approvable.`);
  }
  const binary = atob(preview.canonicalPayloadBase64);
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  if (bytes.byteLength !== preview.payloadBytes) {
    throw new Error('Canonical preview payload length does not match its metadata.');
  }
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  const decoded = JSON.parse(text) as { schemaVersion?: unknown; contentHash?: unknown };
  if (
    decoded.schemaVersion !== preview.schemaVersion ||
    decoded.contentHash !== preview.snapshot.contentHash
  ) {
    throw new Error('Canonical preview payload does not match the displayed snapshot.');
  }
  return bytes;
}

export function canonicalPayloadText(preview: SnapshotPreview): string {
  return new TextDecoder('utf-8', { fatal: true }).decode(canonicalUploadBytes(preview));
}

export function formatCanonicalJson(text: string): string {
  let result = '';
  let depth = 0;
  let inString = false;
  let escaped = false;
  const indent = () => '  '.repeat(depth);
  for (const character of text) {
    if (inString) {
      result += character;
      if (escaped) escaped = false;
      else if (character === '\\') escaped = true;
      else if (character === '"') inString = false;
      continue;
    }
    if (character === '"') {
      inString = true;
      result += character;
    } else if (character === '{' || character === '[') {
      depth += 1;
      result += `${character}\n${indent()}`;
    } else if (character === '}' || character === ']') {
      depth -= 1;
      result += `\n${indent()}${character}`;
    } else if (character === ',') {
      result += `,\n${indent()}`;
    } else if (character === ':') {
      result += ': ';
    } else {
      result += character;
    }
  }
  return result;
}

export type PreviewNotice = { kind: 'redaction' | 'truncation'; path: string; reason: string };

export function previewNotices(preview: SnapshotPreview): PreviewNotice[] {
  if (preview.snapshot == null) return [];
  return [
    ...metadataRows(preview.snapshot.redactions, 'redaction'),
    ...metadataRows(preview.snapshot.truncation, 'truncation'),
  ];
}

function metadataRows(value: JsonValue | undefined, kind: PreviewNotice['kind']): PreviewNotice[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((row) => {
    if (row == null || Array.isArray(row) || typeof row !== 'object') return [];
    const path = row.path;
    const reason = row.reason;
    return typeof path === 'string' && typeof reason === 'string' ? [{ kind, path, reason }] : [];
  });
}

export function frozenCostDisclosure(preview: SnapshotPreview): string {
  const snapshot = preview.snapshot;
  const session = objectValue(snapshot?.session);
  const usage = objectValue(session?.usage);
  const microUsd = typeof usage?.estimatedCostMicroUsd === 'number' ? usage.estimatedCostMicroUsd : 0;
  const models = Array.isArray(usage?.unpricedModels)
    ? usage.unpricedModels.filter((model): model is string => typeof model === 'string')
    : [];
  const cost = `$${(microUsd / 1_000_000).toFixed(2)}`;
  if (models.length > 0) {
    return `${cost} frozen priced estimate; ${models.length} unpriced ${models.length === 1 ? 'model' : 'models'} excluded`;
  }
  return `${cost} frozen estimate; all models priced`;
}

function objectValue(value: JsonValue | undefined): { [key: string]: JsonValue } | undefined {
  return value != null && !Array.isArray(value) && typeof value === 'object' ? value : undefined;
}
