import { useEffect, useRef, useState } from 'react';
import {
  decodeSession,
  sessionDetailRequestPath,
  synthesisRequestPath,
} from '@/pages/coslash/hooks/use-sessions';
import { ApiAuthenticationError, apiFetch, readApiError } from '@/pages/coslash/lib/api';
import { isLocalSession, sessionKey, type Session, type SessionDetail } from '@/pages/coslash/lib/session';
import {
  detailAttemptState,
  detailFailure,
  detailPresentation,
  detailRequestKey,
  overlayLiveSessionFields,
  synthesisAttemptKey,
  synthesisMatchesSnapshot,
  synthesisSettlement,
  type DetailError,
  type DetailErrorKind,
  type DetailFailure,
  type DetailResponse,
  type SynthesisResponse,
} from '@/pages/coslash/lib/session-detail';
import { MINUTE } from '@/pages/coslash/lib/time';

export function useSessionDetail(
  session: Session | null,
  detailRetryToken: number,
  sessionsVersion: number,
  synthesisSettingsKey: string,
): {
  detail: SessionDetail | null;
  isLoading: boolean;
  loadError: string | null;
  loadErrorKind: DetailErrorKind | null;
  cachedOffline: boolean;
  summaryOnly: boolean;
  synthesisSettledCount: number;
} {
  const [loadedDetail, setLoadedDetail] = useState<{
    key: string;
    retryToken: number;
    detail: SessionDetail;
    synthesisRevision: number;
    cachedOffline: boolean;
  } | null>(null);
  const [detailError, setDetailError] = useState<DetailError | null>(null);
  const [loadedSynthesis, setLoadedSynthesis] = useState<({ key: string } & SynthesisResponse) | null>(null);
  const [synthesisSettledCount, setSynthesisSettledCount] = useState(0);
  const pendingSynthesisKey = useRef<string | null>(null);
  const pollDeadline = useRef<{ key: string; deadline: number } | null>(null);
  const detailKey = session == null ? null : detailRequestKey(session);
  const snapshot = loadedDetail?.key === detailKey ? loadedDetail.detail : null;
  const synthesisRevision = snapshot == null ? 0 : (loadedDetail?.synthesisRevision ?? 0);
  const synthesisKey = snapshot == null ? null : synthesisAttemptKey(snapshot);
  const sessionRef = useRef(session);

  useEffect(() => {
    sessionRef.current = session;
  }, [session]);

  useEffect(() => {
    const current = sessionRef.current;
    if (current == null || detailKey == null) return;
    if (current.detailRevision === '') return;

    const controller = new AbortController();
    const load = async () => {
      try {
        const response = await apiFetch(sessionDetailRequestPath(current, 'latest'), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw detailFailure(response.status, (await readApiError(response))?.code);
        }
        const body = (await response.json()) as DetailResponse;
        if (
          body.sourceId !== current.sourceId ||
          body.agent !== current.agent ||
          body.sessionId !== current.id ||
          typeof body.revision !== 'string' ||
          body.revision === '' ||
          body.session == null ||
          typeof body.session !== 'object'
        ) {
          throw {
            kind: 'corrupt',
            message: 'Session details did not match the selected revision.',
          } satisfies DetailFailure;
        }
        const detail = decodeSession({
          ...current,
          ...body.session,
          repo: body.session.repo ?? current.repo,
          sourceId: current.sourceId,
          sourceLabel: current.sourceLabel,
          sourceClass: current.sourceClass,
          logicalSessionId: current.logicalSessionId,
          revision: current.revision,
          detailRevision: body.revision,
          fullRevision: current.fullRevision,
          eligibleForAggregates: current.eligibleForAggregates,
          displayStale: current.displayStale,
          launchable: current.launchable,
          launchBlockReason: current.launchBlockReason,
        });
        if (!controller.signal.aborted) {
          const loaded = {
            key: detailKey,
            retryToken: detailRetryToken,
            detail,
            synthesisRevision: body.synthesisRevision ?? 0,
            cachedOffline: body.cachedOffline,
          };
          setLoadedDetail(loaded);
          setDetailError(null);
        }
      } catch (error: unknown) {
        if (controller.signal.aborted) return;
        if (error instanceof ApiAuthenticationError) {
          setLoadedDetail(null);
          setLoadedSynthesis(null);
          setDetailError({
            key: detailKey,
            retryToken: detailRetryToken,
            kind: 'authentication',
            message: error.message,
          });
          return;
        }
        const failure = error as Partial<DetailFailure>;
        setDetailError({
          key: detailKey,
          retryToken: detailRetryToken,
          kind: failure.kind ?? 'other',
          message: failure.message ?? 'Could not load session details.',
        });
      }
    };
    void load();
    return () => controller.abort();
  }, [detailKey, detailRetryToken]);

  useEffect(() => {
    const current = session;
    if (current == null || synthesisKey == null || snapshot == null) {
      pollDeadline.current = null;
      pendingSynthesisKey.current = null;
      return;
    }
    if (!isLocalSession(current)) {
      pollDeadline.current = null;
      pendingSynthesisKey.current = null;
      return;
    }
    if (pollDeadline.current?.key !== synthesisKey) {
      pollDeadline.current = null;
    }
    const identity = {
      sourceId: current.sourceId,
      agent: current.agent,
      id: current.id,
    };
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      if (controller.signal.aborted) return;
      try {
        const res = await apiFetch(synthesisRequestPath(identity), {
          signal: controller.signal,
        });
        if (!res.ok) return;
        const result = (await res.json()) as SynthesisResponse;
        if (controller.signal.aborted) return;
        const settlement = synthesisSettlement(
          pendingSynthesisKey.current,
          sessionKey(current),
          result,
          synthesisRevision,
        );
        pendingSynthesisKey.current = settlement.pendingKey;
        if (settlement.settled) setSynthesisSettledCount((count) => count + 1);
        if (result.synthesis == null && result.synthesisPending) {
          pollDeadline.current ??= { key: synthesisKey, deadline: Date.now() + 2 * MINUTE };
          if (Date.now() < pollDeadline.current.deadline) {
            timer = setTimeout(load, 3_000);
            if (settlement.matchesSnapshot) setLoadedSynthesis({ key: synthesisKey, ...result });
          } else if (settlement.matchesSnapshot) {
            setLoadedSynthesis({ key: synthesisKey, ...result, synthesisPending: false });
          }
        } else {
          pollDeadline.current = null;
          if (settlement.matchesSnapshot) setLoadedSynthesis({ key: synthesisKey, ...result });
        }
      } catch (error: unknown) {
        // Detail loading owns the visible request error. Synthesis failures
        // remain represented by the endpoint's synthesisError field.
        if (!controller.signal.aborted && error instanceof ApiAuthenticationError) return;
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer != null) clearTimeout(timer);
    };
  }, [synthesisKey, snapshot, synthesisRevision, session, sessionsVersion, synthesisSettingsKey]);

  const presentation = detailPresentation(session);
  if (session == null || detailKey == null) {
    return {
      detail: null,
      isLoading: false,
      loadError: null,
      loadErrorKind: null,
      cachedOffline: false,
      summaryOnly: false,
      synthesisSettledCount,
    };
  }
  if (presentation.summaryOnly) {
    return {
      detail: presentation.detail,
      isLoading: false,
      loadError: null,
      loadErrorKind: null,
      cachedOffline: false,
      summaryOnly: true,
      synthesisSettledCount,
    };
  }
  const attempt = detailAttemptState(loadedDetail, detailError, detailKey, detailRetryToken);
  if (!attempt.hasSnapshot || loadedDetail == null) {
    return {
      detail: null,
      isLoading: attempt.isLoading,
      loadError: attempt.error?.message ?? null,
      loadErrorKind: attempt.error?.kind ?? null,
      cachedOffline: false,
      summaryOnly: false,
      synthesisSettledCount,
    };
  }
  const synthesis =
    loadedSynthesis?.key === synthesisKey && synthesisMatchesSnapshot(loadedSynthesis, synthesisRevision)
      ? loadedSynthesis
      : null;
  const detail = overlayLiveSessionFields(loadedDetail.detail, session);
  return {
    detail:
      synthesis == null
        ? detail
        : {
            ...detail,
            synthesis: synthesis.synthesis,
            synthesisPending: synthesis.synthesisPending,
            synthesisError: synthesis.synthesisError,
          },
    isLoading: attempt.isLoading,
    loadError: attempt.error?.message ?? null,
    loadErrorKind: attempt.error?.kind ?? null,
    cachedOffline: loadedDetail.cachedOffline,
    summaryOnly: false,
    synthesisSettledCount,
  };
}
