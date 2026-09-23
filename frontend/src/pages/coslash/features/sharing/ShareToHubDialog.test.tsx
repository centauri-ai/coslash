import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { ShareResult } from './model';
import { BackupUploadProgress, CompleteBackupDisclosure } from './ShareToHubDialog';
import {
  attemptStillCurrent,
  pendingReviewRecords,
  restoredDraftWindow,
  storeShareDraft,
  type ReviewRecord,
} from './workflow';

describe('complete backup sharing presentation', () => {
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
        ]}
      />,
    );
    expect(markup).toContain('role="status"');
    expect(markup).toContain('overflow-y-auto');
    expect(markup).toContain('resuming');
    expect(markup).toContain('uploading');
    expect(markup.match(/data-testid="backup-upload-progress"/g)).toHaveLength(2);
  });

  it('persists only failed review identities after a partial result', () => {
    const records = [
      { item: { localSessionId: 'local:codex:accepted' } },
      { item: { localSessionId: 'local:codex:failed' } },
    ] as unknown as ReviewRecord[];
    const results: ShareResult['results'] = [
      {
        localSessionId: 'local:codex:accepted',
        idempotencyKey: 'accepted-key-0001',
        state: 'accepted',
        revisionId: 'revision',
        deduplicated: false,
        sharedAt: '2026-09-23T00:00:00Z',
        route: {
          hubContractVersion: 'session-backup-read/v1',
          repositoryId: 'repository',
          path: '/v3/session-backups/revision',
        },
      },
      {
        localSessionId: 'local:codex:failed',
        idempotencyKey: 'failed-key-000001',
        state: 'failed',
        deduplicated: false,
        error: { code: 'temporary_unavailable', retryable: true },
      },
    ];

    expect(pendingReviewRecords(records, results)).toEqual([records[1]]);
  });

  it('clears an explicitly empty draft and preserves its candidate window otherwise', () => {
    const values = new Map<string, string>();
    const storage = {
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    };
    const record = {
      preview: { state: 'ready' },
      item: { localSessionId: 'local:codex:one' },
    } as unknown as ReviewRecord;

    storeShareDraft(storage, [record], true, '30d');
    expect([...values.values()][0]).toContain('"window":"30d"');
    storeShareDraft(storage, [], false, '30d');
    expect(values.size).toBe(0);
    expect(restoredDraftWindow('30d')).toBe('30d');
    expect(restoredDraftWindow(undefined)).toBe('all');
  });

  it('rejects completion from a closed, aborted, or superseded attempt', () => {
    expect(attemptStillCurrent(true, 2, 2, { aborted: false })).toBe(true);
    expect(attemptStillCurrent(false, 2, 2, { aborted: false })).toBe(false);
    expect(attemptStillCurrent(true, 1, 2, { aborted: false })).toBe(false);
    expect(attemptStillCurrent(true, 2, 2, { aborted: true })).toBe(false);
  });
});
