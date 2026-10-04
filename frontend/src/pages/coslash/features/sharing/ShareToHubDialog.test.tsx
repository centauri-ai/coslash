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
  privateNotice,
  toggleCandidateGroup,
  type BackupPreview,
  type ShareCandidate,
  type ShareDestination,
  type ShareItemResult,
  type ShareResult,
} from './model';
import {
  BackupPreparationProgress,
  BackupUploadProgress,
  CompleteBackupDisclosure,
  CompleteBackupSupport,
  ShareCandidatesLoadingStatus,
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

type DialogProps = Parameters<typeof ShareToHubDialog>[0];

function shareDialogProps(overrides: Partial<DialogProps> = {}): DialogProps {
  return {
    open: true,
    onOpenChange: vi.fn(),
    candidates: [],
    candidatesLoading: false,
    candidatesError: null,
    window: '7d',
    onWindowChange: vi.fn(),
    destinationResult: {
      contractVersion: 'hub-share/v1',
      configured: true,
      state: 'ready',
      destination,
    },
    onOpenSettings: vi.fn(),
    onDestinationRefresh: vi.fn(),
    ...overrides,
  };
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

function findComponentProps(root: unknown, type: unknown): Record<string, unknown> | null {
  if (typeof root !== 'object' || root == null || !('props' in root)) return null;
  const node = root as { type?: unknown; props: Record<string, unknown> };
  if (node.type === type) return node.props;
  const children = Array.isArray(node.props.children) ? node.props.children : [node.props.children];
  for (const child of children) {
    const found = findComponentProps(child, type);
    if (found) return found;
  }
  return null;
}

function findInput(root: unknown, type: string): Record<string, unknown> | null {
  if (Array.isArray(root)) {
    for (const child of root) {
      const found = findInput(child, type);
      if (found) return found;
    }
    return null;
  }
  const props = elementProps(root);
  if (!props) return null;
  if (props.type === type && typeof props.onChange === 'function') return props;
  const children = Array.isArray(props.children) ? props.children : [props.children];
  for (const child of children) {
    const found = findInput(child, type);
    if (found) return found;
  }
  return null;
}

function findUploadButton(root: unknown): Record<string, unknown> | null {
  return findButton(root, 'Approve and upload') ?? findButton(root, 'Exercise fixture result');
}

function findButton(root: unknown, label: string): Record<string, unknown> | null {
  const props = elementProps(root);
  if (!props) return null;
  if (typeof props.onClick === 'function' && textContent(props.children).includes(label)) {
    return props;
  }
  const children = Array.isArray(props.children) ? props.children : [props.children];
  for (const child of children) {
    const found = findButton(child, label);
    if (found) return found;
  }
  return null;
}

function findActionButton(root: unknown, label: string): Record<string, unknown> | null {
  return findButton(root, label);
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

  it('announces candidate loading and the remote-refresh wait as separate stages', () => {
    const loading = renderToStaticMarkup(<ShareCandidatesLoadingStatus stage="loading" />);
    const refreshing = renderToStaticMarkup(<ShareCandidatesLoadingStatus stage="refreshing" />);
    expect(loading).toContain('role="status"');
    expect(loading).toContain('Loading sessions available to share');
    expect(refreshing).toContain('Waiting for connected workspaces to finish refreshing');
  });

  it('shows actual prepared-item counts without a fabricated percentage', () => {
    const markup = renderToStaticMarkup(<BackupPreparationProgress completed={1} total={3} />);
    expect(markup).toContain('role="status"');
    expect(markup).toContain('1 of 3 ready');
    expect(markup).not.toContain('%');
  });

  it('renders candidate feedback in the initial loading state', () => {
    installStorage();
    hooks.reset();
    const rendered = hooks.render(
      ShareToHubDialog,
      shareDialogProps({ candidatesLoading: true, candidatesLoadStage: 'loading' }),
    );
    expect(findComponentProps(rendered, ShareCandidatesLoadingStatus)).toMatchObject({ stage: 'loading' });
  });

  it('shows an empty eligible-session result after loading finishes', () => {
    installStorage();
    hooks.reset();
    const rendered = hooks.render(ShareToHubDialog, shareDialogProps({ window: 'all' }));
    expect(textContent(rendered)).toContain('No eligible sessions are available in this time window.');
    expect(findComponentProps(rendered, ShareCandidatesLoadingStatus)).toBeNull();
  });

  it('offers retry after candidate loading fails', () => {
    installStorage();
    const retry = vi.fn();
    hooks.reset();
    const rendered = hooks.render(
      ShareToHubDialog,
      shareDialogProps({
        candidatesError: 'Could not load sessions.',
        candidatesLoadStage: 'error',
        onRetryCandidates: retry,
      }),
    );
    const button = findActionButton(rendered, 'Retry loading sessions');
    expect(button).not.toBeNull();
    (button!.onClick as () => void)();
    expect(retry).toHaveBeenCalledOnce();
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
      contractVersion: 'hub-share/v2',
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

  it('removes privately completed items from the retry draft', () => {
    const record = reviewRecord(candidate('private-id'), 'original-private-key-0001');
    const result: ShareItemResult = {
      localSessionId: record.item.localSessionId,
      idempotencyKey: record.item.idempotencyKey,
      state: 'private',
      private: true,
      deduplicated: true,
      sharingNotice: 'This backup is private in My space.',
    };
    expect(updateRetryDraft({ records: [record], renewedReviewIds: new Set() }, result).records).toEqual([]);
  });

  it('clearly labels private completion and never renders the local session ID', async () => {
    installStorage();
    const value = candidate('opaque-private-id');
    value.session.name = 'Quarterly planning';
    const record = reviewRecord(value, 'private-fixture-key-0001');
    storeDraft([record], true);
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
      fixtureMode: true,
      fixtureOutcome: 'private' as const,
    };
    hooks.reset();
    hooks.render(ShareToHubDialog, props);
    const beforeSubmit = hooks.render(ShareToHubDialog, props);
    const approve = findUploadButton(beforeSubmit);
    expect(approve).not.toBeNull();
    if (!approve) throw new Error('Fixture approval button was not rendered.');
    await (approve.onClick as () => Promise<void>)();
    const rendered = hooks.render(ShareToHubDialog, props);
    const text = textContent(rendered);
    expect(text).toContain('Backup completed privately');
    expect(text).toContain('Quarterly planning');
    expect(text).toContain('not visible to members of Compiler Team');
    expect(text).toContain('explicitly share it if you choose');
    expect(text).not.toContain('opaque-private-id');
    expect(text).not.toContain(record.item.localSessionId);
  });

  it('preserves prior accepted items when the failed item succeeds on same-key retry', async () => {
    installStorage();
    const first = candidate('opaque-accepted-id');
    const retry = candidate('opaque-retry-id');
    first.session.name = 'Accepted notes';
    retry.session.name = 'Retry notes';
    storeDraft(
      [reviewRecord(first, 'fixture-accepted-key-0001'), reviewRecord(retry, 'fixture-retry-key-0001')],
      true,
    );
    const props = {
      open: true,
      onOpenChange: vi.fn(),
      candidates: [first, retry],
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
      fixtureMode: true,
      fixtureOutcome: 'partial' as const,
    };
    hooks.reset();
    hooks.render(ShareToHubDialog, props);
    let rendered = hooks.render(ShareToHubDialog, props);
    const approve = findUploadButton(rendered);
    expect(approve).not.toBeNull();
    if (!approve) throw new Error('Fixture approval button was not rendered.');
    await (approve.onClick as () => Promise<void>)();
    rendered = hooks.render(ShareToHubDialog, props);
    expect(textContent(rendered)).toContain('1 shared with Compiler Team');
    expect(textContent(rendered)).toContain('1 failed item');

    const retryButton = findButton(rendered, 'Retry failed with same key');
    expect(retryButton).not.toBeNull();
    if (!retryButton) throw new Error('Same-key retry button was not rendered.');
    await (retryButton.onClick as () => Promise<void>)();
    rendered = hooks.render(ShareToHubDialog, props);
    const retryApproval = findUploadButton(rendered);
    expect(retryApproval).not.toBeNull();
    if (!retryApproval) throw new Error('Retry approval button was not rendered.');
    await (retryApproval.onClick as () => Promise<void>)();

    const finalText = textContent(hooks.render(ShareToHubDialog, props));
    expect(finalText).toContain('Share accepted');
    expect(finalText).toContain('2 complete backups were shared with Compiler Team.');
    expect(finalText).toContain('Accepted notes');
    expect(finalText).toContain('Retry notes');
    expect(finalText).not.toContain('opaque-accepted-id');
    expect(finalText).not.toContain('opaque-retry-id');
    expect(api.submitHubShare).not.toHaveBeenCalled();
  });

  it('explains identical retryable failures and shows same-key recovery by readable name', async () => {
    installStorage();
    const first = candidate('opaque-failure-one');
    const second = candidate('opaque-failure-two');
    first.session.name = 'Planning notes';
    second.session.name = 'Review notes';
    const records = [
      reviewRecord(first, 'retry-fixture-key-one-0001'),
      reviewRecord(second, 'retry-fixture-key-two-0001'),
    ];
    storeDraft(records, true);
    const props = {
      open: true,
      onOpenChange: vi.fn(),
      candidates: [first, second],
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
      fixtureMode: true,
      fixtureOutcome: 'failed' as const,
    };
    hooks.reset();
    hooks.render(ShareToHubDialog, props);
    const beforeSubmit = hooks.render(ShareToHubDialog, props);
    const approve = findUploadButton(beforeSubmit);
    expect(approve).not.toBeNull();
    if (!approve) throw new Error('Fixture approval button was not rendered.');
    await (approve.onClick as () => Promise<void>)();
    const rendered = hooks.render(ShareToHubDialog, props);
    const text = textContent(rendered);
    expect(text).toContain('None of the 2 selected sessions were shared with Compiler Team');
    expect(text).toContain('Local could not confirm the upload result with Hub.');
    expect(text).toContain('Keep failed items selected and retry with their original keys.');
    expect(text).toContain('Planning notes');
    expect(text).toContain('Review notes');
    expect(text).toContain('Same-key retry available');
    expect(text).not.toContain('opaque-failure-one');
    expect(text).not.toContain('opaque-failure-two');
  });

  it('requires explicit approval before sending a reviewed backup', async () => {
    installStorage();
    const value = candidate('requires-approval');
    const record = reviewRecord(value, 'original-key-requires-approval');
    storeDraft([record], false);
    const props = shareDialogProps({ candidates: [value], window: 'all' });
    hooks.reset();
    hooks.render(ShareToHubDialog, props);
    const rendered = hooks.render(ShareToHubDialog, props);
    const uploadButton = findUploadButton(rendered);
    expect(uploadButton).not.toBeNull();
    expect(uploadButton?.disabled).toBe(true);
    await (uploadButton!.onClick as () => Promise<void>)();
    expect(api.submitHubShare).not.toHaveBeenCalled();
  });

  it('ignores a backup preview that finishes after the dialog closes', async () => {
    installStorage();
    const value = candidate('stale-preview');
    let resolvePreview = (_preview: BackupPreview) => {};
    api.prepareBackup.mockReturnValue(
      new Promise<BackupPreview>((resolve) => {
        resolvePreview = resolve;
      }),
    );
    const props = shareDialogProps({ candidates: [value], window: 'all' });
    hooks.reset();
    let rendered = hooks.render(ShareToHubDialog, props);
    expect(textContent(rendered)).toContain('Session stale-preview');
    const checkbox = findInput(rendered, 'checkbox');
    expect(checkbox).not.toBeNull();
    (checkbox!.onChange as () => void)();
    rendered = hooks.render(ShareToHubDialog, props);
    const reviewButton = findActionButton(rendered, 'See what gets shared');
    expect(reviewButton).not.toBeNull();
    const completion = (reviewButton!.onClick as () => Promise<void>)();
    expect(api.prepareBackup).toHaveBeenCalledOnce();

    const rootProps = elementProps(rendered);
    (rootProps!.onOpenChange as (open: boolean) => void)(false);
    hooks.render(ShareToHubDialog, shareDialogProps({ open: false, candidates: [value] }));
    rendered = hooks.render(ShareToHubDialog, props);
    resolvePreview(reviewRecord(value, 'original-key-stale-preview').preview);
    await completion;
    rendered = hooks.render(ShareToHubDialog, props);

    expect(textContent(rendered)).not.toContain('Review binds each complete-backup hash');
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
    const duplicate = (uploadButton.onClick as () => Promise<void>)();
    expect(api.submitHubShare).toHaveBeenCalledOnce();

    hooks.render(ShareToHubDialog, { ...props, open: false });
    hooks.render(ShareToHubDialog, props);
    rendered = hooks.render(ShareToHubDialog, props);
    const reopenedDraft = [...values.values()][0];
    expect(textContent(rendered)).toContain('Review binds each complete-backup hash');

    resolveUpload({
      contractVersion: 'hub-share/v2',
      requestId: 'stale-request',
      state: 'succeeded',
      results: [accepted(record)],
    });
    await completion;
    await duplicate;
    rendered = hooks.render(ShareToHubDialog, props);

    expect([...values.values()][0]).toBe(reopenedDraft);
    expect(textContent(rendered)).toContain('Review binds each complete-backup hash');
    expect(textContent(rendered)).not.toContain('Complete backups accepted');
  });
});

describe('privateNotice', () => {
  it('shows the Hub notice only for an accepted private backup', () => {
    const accepted = {
      localSessionId: 'local:codex:one',
      idempotencyKey: 'key-000000000000',
      state: 'accepted' as const,
      revisionId: 'revision-one',
      deduplicated: false,
      sharedAt: '2026-09-22T20:00:00Z',
      route: {
        hubContractVersion: 'session-backup-read/v1' as const,
        repositoryId: 'repository-one',
        path: '/v3/session-backups/revision-one',
      },
    };
    expect(privateNotice([accepted])).toBeUndefined();
    expect(
      privateNotice([{ ...accepted, private: true, sharingNotice: 'This backup is private in My space.' }]),
    ).toBe('This backup is private in My space.');
    expect(privateNotice([{ ...accepted, private: false, sharingNotice: 'ignored' }])).toBeUndefined();
  });
});
