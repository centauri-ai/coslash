import {
  consentStillCurrent,
  isCompleteBackupCandidate,
  localSessionId,
  RETRY_RULES,
  type BackupPreview,
  type DestinationResult,
  type ShareCandidate,
  type ShareItemRequest,
  type ShareResult,
} from './model';

export const COMPLETE_BACKUP_DRAFT_STORAGE_KEY = 'coslash:complete-backup-share-draft:v1';

export type ReviewRecord = {
  candidate: ShareCandidate;
  preview: BackupPreview;
  item: ShareItemRequest;
};

export type RetryDraft = { records: ReviewRecord[]; renewedReviewIds: Set<string> };

export function retainRetryDraftSelection(draft: RetryDraft, selected: ReadonlySet<string>): RetryDraft {
  return {
    records: draft.records.filter((record) => selected.has(record.item.localSessionId)),
    renewedReviewIds: new Set([...draft.renewedReviewIds].filter((id) => selected.has(id))),
  };
}

export function storeDraft(records: ReviewRecord[], reviewed: boolean, renewedReviewIds = new Set<string>()) {
  try {
    if (records.length === 0 && renewedReviewIds.size === 0) {
      localStorage.removeItem(COMPLETE_BACKUP_DRAFT_STORAGE_KEY);
      return;
    }
    localStorage.setItem(
      COMPLETE_BACKUP_DRAFT_STORAGE_KEY,
      JSON.stringify({
        reviewed,
        records: records.map(({ preview, item }) => ({ preview, item })),
        renewedReviewSessionIds: [...renewedReviewIds],
      }),
    );
  } catch {
    // The frozen collector spool remains retryable even when browser storage
    // is unavailable; this only disables automatic dialog restoration.
  }
}

export function updateRetryDraft(draft: RetryDraft, item: ShareResult['results'][number]): RetryDraft {
  const records = [...draft.records];
  const renewedReviewIds = new Set(draft.renewedReviewIds);
  if (item.state !== 'failed' || !item.error.retryable) {
    return {
      records: records.filter((record) => record.item.localSessionId !== item.localSessionId),
      renewedReviewIds: new Set([...renewedReviewIds].filter((id) => id !== item.localSessionId)),
    };
  }
  if (RETRY_RULES[item.error.code].renewedReview) {
    renewedReviewIds.add(item.localSessionId);
    return {
      records: records.filter((record) => record.item.localSessionId !== item.localSessionId),
      renewedReviewIds,
    };
  }
  return { records, renewedReviewIds };
}

export function retryDraftForResult(records: ReviewRecord[], result: ShareResult): RetryDraft {
  return result.results.reduce<RetryDraft>(updateRetryDraft, {
    records,
    renewedReviewIds: new Set(),
  });
}

export function restoreDraft(
  raw: string,
  candidates: ShareCandidate[],
  destination: NonNullable<Extract<DestinationResult, { state: 'ready' }>['destination']>,
): { records: ReviewRecord[]; renewedReviewIds: Set<string>; reviewed: boolean } | null {
  const stored = JSON.parse(raw) as {
    reviewed?: boolean;
    records?: { preview?: BackupPreview; item?: ShareItemRequest }[];
    renewedReviewSessionIds?: string[];
  };
  const records: ReviewRecord[] = [];
  const renewedReviewIds = new Set<string>();
  for (const value of stored.records ?? []) {
    if (!value.preview || !value.item) continue;
    const candidate = candidates.find(
      ({ session }) => localSessionId(session) === value.item?.localSessionId,
    );
    if (
      candidate &&
      isCompleteBackupCandidate(candidate) &&
      consentStillCurrent(value.item, candidate.session, value.preview, destination)
    ) {
      records.push({ candidate, preview: value.preview, item: value.item });
    } else if (candidate && isCompleteBackupCandidate(candidate)) {
      renewedReviewIds.add(value.item.localSessionId);
    }
  }
  for (const id of stored.renewedReviewSessionIds ?? []) {
    const candidate = candidates.find(({ session }) => localSessionId(session) === id);
    if (candidate && isCompleteBackupCandidate(candidate)) renewedReviewIds.add(id);
  }
  if (records.length === 0 && renewedReviewIds.size === 0) return null;
  return {
    records,
    renewedReviewIds,
    reviewed: renewedReviewIds.size === 0 && stored.reviewed === true,
  };
}
