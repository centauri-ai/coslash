import type { BackupPreview, ShareCandidate, ShareItemRequest, ShareResult, ShareWindow } from './model';

export type ReviewRecord = {
  candidate: ShareCandidate;
  preview: BackupPreview;
  item: ShareItemRequest;
};

export const DRAFT_STORAGE_KEY = 'coslash:complete-backup-share-draft:v1';

export function storeShareDraft(
  storage: Pick<Storage, 'removeItem' | 'setItem'>,
  records: ReviewRecord[],
  reviewed: boolean,
  window: ShareWindow,
) {
  try {
    if (records.length === 0) {
      storage.removeItem(DRAFT_STORAGE_KEY);
      return;
    }
    storage.setItem(
      DRAFT_STORAGE_KEY,
      JSON.stringify({
        reviewed,
        window,
        records: records.map(({ preview, item }) => ({ preview, item })),
      }),
    );
  } catch {
    // The frozen collector spool remains retryable when browser storage is unavailable.
  }
}

export function restoredDraftWindow(value: unknown): ShareWindow {
  return value === '7d' || value === '30d' || value === 'all' ? value : 'all';
}

export function pendingReviewRecords(
  records: ReviewRecord[],
  results: ShareResult['results'],
): ReviewRecord[] {
  const accepted = new Set(
    results.filter((result) => result.state !== 'failed').map((result) => result.localSessionId),
  );
  return records.filter((record) => !accepted.has(record.item.localSessionId));
}

export function attemptStillCurrent(
  open: boolean,
  generation: number,
  currentGeneration: number,
  signal: Pick<AbortSignal, 'aborted'>,
): boolean {
  return open && generation === currentGeneration && !signal.aborted;
}
