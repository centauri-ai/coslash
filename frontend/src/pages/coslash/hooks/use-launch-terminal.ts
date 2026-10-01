import { useState } from 'react';
import { apiFetch, readApiError } from '@/pages/coslash/lib/api';
import { copyHandoffText, cursorHandoffText } from '@/pages/coslash/lib/handoff';
import { isLocalSession, type SessionDetail, type SessionIdentity } from '@/pages/coslash/lib/session';

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

export function useLaunchTerminal(session: SessionIdentity) {
  const [launchError, setLaunchError] = useState<string | null>(null);

  const launch = (mode: LaunchMode, handoff?: string) => {
    setLaunchError(null);
    launchTerminal(session, mode, handoff).catch((error: unknown) => {
      setLaunchError(error instanceof Error ? error.message : String(error));
    });
  };

  return { launch, launchError };
}
