import { useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';

export type LaunchMode = 'resume' | 'new';

async function launchTerminal(sessionId: string, mode: LaunchMode, handoff?: string): Promise<void> {
  const response = await apiFetch(`/api/launch?id=${sessionId}&mode=${mode}`, {
    method: 'POST',
    body: handoff,
  });
  if (!response.ok) {
    const reason = (await response.text()).trim();
    throw new Error(reason || `Launch failed (${response.status})`);
  }
}

export function useLaunchTerminal(sessionId: string) {
  const [launchError, setLaunchError] = useState<string | null>(null);

  const launch = (mode: LaunchMode, handoff?: string) => {
    setLaunchError(null);
    launchTerminal(sessionId, mode, handoff).catch((error: unknown) => {
      setLaunchError(error instanceof Error ? error.message : String(error));
    });
  };

  return { launch, launchError };
}
