import { useState } from 'react';
import { apiFetch, readApiError } from '@/pages/coslash/lib/api';
import type { SessionIdentity } from '@/pages/coslash/lib/session';

export type LaunchMode = 'resume' | 'new';

export function launchRequestPath(session: SessionIdentity, mode: LaunchMode): string {
  return `/api/launch?${new URLSearchParams({
    source: session.sourceId,
    agent: session.agent,
    id: session.id,
    mode,
  })}`;
}

async function launchTerminal(session: SessionIdentity, mode: LaunchMode, handoff?: string): Promise<void> {
  const response = await apiFetch(launchRequestPath(session, mode), {
    method: 'POST',
    body: handoff,
  });
  if (!response.ok) {
    const apiError = await readApiError(response);
    throw new Error(apiError?.error || `Launch failed (${response.status})`);
  }
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
