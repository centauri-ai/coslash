import { useEffect, useRef, useState } from 'react';
import { apiFetch, readApiError } from '@/pages/coslash/lib/api';
import { copyHandoffText, cursorHandoffText } from '@/pages/coslash/lib/handoff';
import {
  isLocalSession,
  sessionKey,
  type SessionDetail,
  type SessionIdentity,
} from '@/pages/coslash/lib/session';

export type LaunchMode = 'resume' | 'new' | 'open';

export function launchRequestPath(session: SessionIdentity, mode: LaunchMode): string {
  return `/api/launch?${new URLSearchParams({
    source: session.sourceId,
    agent: session.agent,
    id: session.id,
    mode,
  })}`;
}

export function launchRequestBody(session: SessionIdentity, handoff?: string): string | undefined {
  return session.agent === 'cursor' ? undefined : handoff;
}

async function launchTerminal(session: SessionIdentity, mode: LaunchMode, handoff?: string): Promise<void> {
  const response = await apiFetch(launchRequestPath(session, mode), {
    method: 'POST',
    body: launchRequestBody(session, handoff),
  });
  if (!response.ok) {
    const apiError = await readApiError(response);
    throw new Error(apiError?.error || `Launch failed (${response.status})`);
  }
}

export async function launchFreshSession(
  session: SessionIdentity & Pick<SessionDetail, 'entrypoint'>,
  brief: string,
): Promise<void> {
  const needsClipboard = isLocalSession(session) && session.agent === 'cursor';
  try {
    await copyHandoffText(needsClipboard ? cursorHandoffText(brief) : brief);
  } catch {
    if (needsClipboard) throw new Error('Could not copy the handoff. Allow clipboard access and try again.');
  }
  const opensCursor = needsClipboard && session.entrypoint === 'cursor-ide';
  await launchTerminal(session, opensCursor ? 'open' : 'new', brief);
}

export type LaunchView = {
  launching: boolean;
  error: string | null;
  sessionKey: string;
  attempt: number;
};

export function idleLaunch(key: string): LaunchView {
  return { launching: false, error: null, sessionKey: key, attempt: 0 };
}

export function startLaunch(state: LaunchView, key: string): LaunchView | null {
  if (state.launching) return null;
  return { launching: true, error: null, sessionKey: key, attempt: state.attempt + 1 };
}

export function settleLaunch(
  state: LaunchView,
  started: { sessionKey: string; attempt: number },
  error: string | null,
): LaunchView {
  if (state.attempt !== started.attempt || state.sessionKey !== started.sessionKey) return state;
  return { ...state, launching: false, error };
}

export function retargetLaunch(state: LaunchView, key: string): LaunchView {
  if (state.sessionKey === key) return state;
  return { launching: false, error: null, sessionKey: key, attempt: state.attempt + 1 };
}

export function useLaunchTerminal(session: SessionIdentity) {
  const key = sessionKey(session);
  const [view, setView] = useState(() => idleLaunch(key));
  const stateRef = useRef(view);

  const apply = (next: LaunchView) => {
    stateRef.current = next;
    setView(next);
  };

  useEffect(() => {
    const next = retargetLaunch(stateRef.current, key);
    if (next !== stateRef.current) apply(next);
  }, [key]);

  const launch = (mode: LaunchMode, handoff?: string) => {
    const next = startLaunch(stateRef.current, key);
    if (next == null) return;
    apply(next);
    const started = { sessionKey: next.sessionKey, attempt: next.attempt };
    void launchTerminal(session, mode, handoff).then(
      () => apply(settleLaunch(stateRef.current, started, null)),
      (error: unknown) => {
        const message = error instanceof Error ? error.message : String(error);
        apply(settleLaunch(stateRef.current, started, message));
      },
    );
  };

  return { launch, launchError: view.error, launching: view.launching };
}
