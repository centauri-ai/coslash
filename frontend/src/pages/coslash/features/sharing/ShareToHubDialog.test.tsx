import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '@/pages/coslash/lib/session';
import {
  restoreDraft,
  retainRetryDraftSelection,
  retryDraftForResult,
  storeDraft,
  updateRetryDraft,
  type ReviewRecord,
} from './draft';
import {
  backupSelection,
  bindBackupConsent,
  filterShareCandidates,
  localSessionId,
  toggleCandidateGroup,
  type BackupPreview,
  type ShareCandidate,
  type ShareDestination,
  type ShareItemResult,
  type ShareResult,
} from './model';
import {
  BackupUploadProgress,
  CompleteBackupDisclosure,
  CompleteBackupSupport,
  ShareToHubDialog,
} from './ShareToHubDialog';

const hooks = vi.hoisted(() => {
  let cursor = 0;
  let values: unknown[] = [];
  let dependencies: (readonly unknown[] | undefined)[] = [];
  let effects: (() => void)[] = [];
  const changed = (index: number, next?: readonly unknown[]) => {
    const prior = dependencies[index];
    return (
      prior == null ||
      next == null ||
      prior.length !== next.length ||
      prior.some((value, dependencyIndex) => !Object.is(value, next[dependencyIndex]))
    );
  };
  return {
    reset() {
      values = [];
      dependencies = [];
    },
    useState(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = typeof initial === 'function' ? initial() : initial;
      return [
        values[index],
        (next: unknown) => {
          values[index] = typeof next === 'function' ? next(values[index]) : next;
        },
      ];
    },
    useRef(initial: unknown) {
      const index = cursor++;
      if (!(index in values)) values[index] = { current: initial };
      return values[index];
    },
    useMemo(factory: () => unknown, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) values[index] = factory();
      dependencies[index] = next;
      return values[index];
    },
    useEffect(effect: () => void, next?: readonly unknown[]) {
      const index = cursor++;
      if (changed(index, next)) effects.push(effect);
      dependencies[index] = next;
    },
    render<Props>(component: (props: Props) => unknown, props: Props) {
      cursor = 0;
      effects = [];
      const root = component(props);
      for (const effect of effects) effect();
      return root;
    },
  };
});

const api = vi.hoisted(() => ({
  beginHubPairing: vi.fn(),
  pollHubPairing: vi.fn(),
  prepareBackup: vi.fn(),
  submitHubShare: vi.fn(),
}));

vi.mock('react', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react')>()),
  useEffect: hooks.useEffect,
  useMemo: hooks.useMemo,
  useRef: hooks.useRef,
  useState: hooks.useState,
}));
vi.mock('./api', () => api);

const destination: ShareDestination = {
  workspaceId: 'workspace-1',
  workspaceName: 'Compiler Team',
  currentMemberCount: 2,
  resultingMemberCount: 2,
  currentApprovedSessionCount: 0,
  historyDisclosure: 'Current members',
  credentialState: 'paired',
  audienceVersion: 'audience-v1',
};

function candidate(id: string, agent = 'codex'): ShareCandidate {
  return {
    session: {
      sourceId: 'local',
      sourceLabel: 'Local Mac',
      agent,
      id,
      name: `Session ${id}`,
      repo: 'coslash',
      branch: 'main',
      mtime: 123,
    } as Session,
    previouslyShared: false,
  };
}

function reviewRecord(value: ShareCandidate, key: string): ReviewRecord {
  const hash = value.session.id.padEnd(64, 'a').slice(0, 64);
  const preview: BackupPreview = {
    adapterVersion: 'backup-preview/v1',
    state: 'ready',
    approvalAllowed: true,
    selection: backupSelection(value.session),
    bundleId: hash,
    sourceRevision: `source-${value.session.id}`,
    coverage: { artifactCount: 1, artifactCounts: [], totalBytes: 10, revisionSha256: hash, problems: [] },
    capability: {
      serverId: 'server-v3',
      maxBackupBytes: 100,
      maxBackupChunkBytes: 10,
      backupWorkspaceBytes: 1000,
      backupUploadExpiresSeconds: 3600,
    },
    audienceVersion: destination.audienceVersion,
  };
  return {
    candidate: value,
    preview,
    item: bindBackupConsent(value.session, preview, destination, key),
  };
}

function accepted(record: ReviewRecord): ShareItemResult {
  return {
    localSessionId: record.item.localSessionId,
    idempotencyKey: record.item.idempotencyKey,
    state: 'accepted',
    revisionId: `revision-${record.candidate.session.id}`,
    deduplicated: false,
    sharedAt: '2026-09-22T20:00:00Z',
    route: {
      hubContractVersion: 'session-backup-read/v1',
      repositoryId: 'repository-1',
      path: `/v3/session-backups/revision-${record.candidate.session.id}`,
    },
  };
}

function failed(
  record: ReviewRecord,
  code: 'temporary_unavailable' | 'stale_backup_review',
): ShareItemResult {
  return {
    localSessionId: record.item.localSessionId,
    idempotencyKey: record.item.idempotencyKey,
    state: 'failed',
    deduplicated: false,
    error: { code, retryable: true },
  };
}

function installStorage() {
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  return values;
}

function elementProps(value: unknown): Record<string, unknown> | null {
  if (typeof value !== 'object' || value == null || !('props' in value)) return null;
  return (value as { props: Record<string, unknown> }).props;
}

function textContent(value: unknown): string {
  if (typeof value === 'string' || typeof value === 'number') return String(value);
  if (Array.isArray(value)) return value.map(textContent).join('');
  const props = elementProps(value);
  return props ? textContent(props.children) : '';
}

function findUploadButton(root: unknown): Record<string, unknown> | null {
  const props = elementProps(root);
  if (!props) return null;
  if (typeof props.onClick === 'function' && textContent(props.children).includes('Approve and upload')) {
    return props;
  }
  const children = Array.isArray(props.children) ? props.children : [props.children];
  for (const child of children) {
    const found = findUploadButton(child);
    if (found) return found;
  }
  return null;
}

describe('complete backup sharing presentation', () => {
  afterEach(() => {
    vi.clearAllMocks();
    vi.unstubAllGlobals();
  });

  it('discloses unredacted secret-bearing content and team visibility', () => {
    const markup = renderToStaticMarkup(<CompleteBackupDisclosure workspaceName="Compiler Team" />);
    expect(markup).toContain('COMPLETE UNREDACTED BACKUP');
    expect(markup).toContain('credentials or other secrets');
    expect(markup).toContain('does not redact');
    expect(markup).toContain('Active members of Compiler Team');
    expect(markup).toContain('data-testid="complete-backup-disclosure"');
  });

  it('announces per-item resuming and uploading progress in a narrow-safe list', () => {
    const markup = renderToStaticMarkup(
      <BackupUploadProgress
        items={[
          { id: 'one', label: 'First session', state: 'resuming' },
          { id: 'two', label: 'Second session', state: 'uploading' },
          { id: 'three', label: 'Completed session', state: 'accepted' },
        ]}
      />,
    );
    expect(markup).toContain('role="status"');
    expect(markup).toContain('overflow-y-auto');
    expect(markup).toContain('resuming');
    expect(markup).toContain('uploading');
    expect(markup).toContain('accepted');
    expect(markup.match(/data-testid="backup-upload-progress"/g)).toHaveLength(3);
  });

  it('keeps mixed-agent rows visible while group selection excludes unsupported agents', () => {
    const codex = candidate('supported');
    const claude = candidate('unsupported', 'claude');
    const visible = filterShareCandidates([codex, claude], '', 'all');
    expect(visible.map(({ session }) => session.id)).toEqual(['supported', 'unsupported']);
    expect([...toggleCandidateGroup(new Set(), visible)]).toEqual([localSessionId(codex.session)]);
    const markup = renderToStaticMarkup(<CompleteBackupSupport candidate={claude} />);
    expect(markup).toContain('data-testid="complete-backup-unsupported"');
    expect(markup).toContain('local and SSH Codex sessions only');
    expect(renderToStaticMarkup(<CompleteBackupSupport candidate={codex} />)).toBe('');
  });

  it('reopens accepted-then-failed batches with only retry-eligible items and original keys', () => {
    const values = installStorage();
    const first = reviewRecord(candidate('first'), 'original-key-first-0001');
    const retry = reviewRecord(candidate('retry'), 'original-key-retry-0001');
    const renew = reviewRecord(candidate('renew'), 'original-key-renew-0001');
    const result: ShareResult = {
      contractVersion: 'hub-share/v1',
      requestId: 'request-1',
      state: 'partial',
      results: [
        accepted(first),
        failed(retry, 'temporary_unavailable'),
        failed(renew, 'stale_backup_review'),
      ],
    };
    const draft = retryDraftForResult([first, retry, renew], result);
    storeDraft(draft.records, false, draft.renewedReviewIds);
    const raw = [...values.values()][0];
    expect(raw).toBeDefined();
    const restored = restoreDraft(raw!, [first.candidate, retry.candidate, renew.candidate], destination);
    expect(restored?.records.map(({ item }) => item.idempotencyKey)).toEqual(['original-key-retry-0001']);
    expect([...restored!.renewedReviewIds]).toEqual([renew.item.localSessionId]);
    expect(restored?.reviewed).toBe(false);
    expect(raw).not.toContain('original-key-first-0001');
  });

  it('reopens accepted-then-transport-error batches without the accepted item', () => {
    const values = installStorage();
    const first = reviewRecord(candidate('first'), 'original-key-first-0001');
    const pending = reviewRecord(candidate('pending'), 'original-key-pending-0001');
    const draft = updateRetryDraft(
      { records: [first, pending], renewedReviewIds: new Set() },
      accepted(first),
    );
    storeDraft(draft.records, true, draft.renewedReviewIds);
    const raw = [...values.values()][0];
    expect(raw).toBeDefined();
    const restored = restoreDraft(raw!, [first.candidate, pending.candidate], destination);
    expect(restored?.records.map(({ item }) => item.idempotencyKey)).toEqual(['original-key-pending-0001']);
    expect(restored?.reviewed).toBe(true);
    expect(raw).not.toContain('original-key-first-0001');
  });

  it('retains renewed-review markers only while their sessions remain selected', () => {
    const pending = reviewRecord(candidate('pending'), 'original-key-pending-0001');
    const renewedId = localSessionId(candidate('renew').session);
    const draft = { records: [pending], renewedReviewIds: new Set([renewedId]) };
    const retained = retainRetryDraftSelection(draft, new Set([pending.item.localSessionId, renewedId]));
    expect(retained.records).toEqual([pending]);
    expect([...retained.renewedReviewIds]).toEqual([renewedId]);
    expect(
      retainRetryDraftSelection(draft, new Set([pending.item.localSessionId])).renewedReviewIds.size,
    ).toBe(0);
  });

  it('ignores an upload response completed after the dialog closes and reopens', async () => {
    const values = installStorage();
    const value = candidate('stale-attempt');
    const record = reviewRecord(value, 'original-key-stale-attempt');
    storeDraft([record], true);
    let resolveUpload = (_result: ShareResult) => {};
    api.submitHubShare.mockReturnValue(
      new Promise<ShareResult>((resolve) => {
        resolveUpload = resolve;
      }),
    );
    const props = {
      open: true,
      onOpenChange: vi.fn(),
      candidates: [value],
      candidatesLoading: false,
      candidatesError: null,
      window: 'all' as const,
      onWindowChange: vi.fn(),
      destinationResult: {
        contractVersion: 'hub-share/v1' as const,
        configured: true,
        state: 'ready' as const,
        destination,
      },
      onOpenSettings: vi.fn(),
      onDestinationRefresh: vi.fn(),
    };

    hooks.reset();
    hooks.render(ShareToHubDialog, props);
    let rendered = hooks.render(ShareToHubDialog, props);
    const uploadButton = findUploadButton(rendered);
    expect(uploadButton).not.toBeNull();
    if (!uploadButton) throw new Error('Upload button was not rendered.');
    const completion = (uploadButton.onClick as () => Promise<void>)();
    expect(api.submitHubShare).toHaveBeenCalledOnce();

    hooks.render(ShareToHubDialog, { ...props, open: false });
    hooks.render(ShareToHubDialog, props);
    rendered = hooks.render(ShareToHubDialog, props);
    const reopenedDraft = [...values.values()][0];
    expect(textContent(rendered)).toContain('Review binds each complete-backup hash');

    resolveUpload({
      contractVersion: 'hub-share/v1',
      requestId: 'stale-request',
      state: 'succeeded',
      results: [accepted(record)],
    });
    await completion;
    rendered = hooks.render(ShareToHubDialog, props);

    expect([...values.values()][0]).toBe(reopenedDraft);
    expect(textContent(rendered)).toContain('Review binds each complete-backup hash');
    expect(textContent(rendered)).not.toContain('Complete backups accepted');
  });
});
