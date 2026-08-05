import { useEffect, useState } from 'react';
import type { Diagnostics } from '@/pages/coslash/lib/diagnostics';

export function useDiagnostics(enabled: boolean) {
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [requestID, setRequestID] = useState(0);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    setIsLoading(true);
    setLoadFailed(false);
    fetch('/api/diagnostics', { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Diagnostics request failed (${response.status})`);
        return response.json() as Promise<Diagnostics>;
      })
      .then((loaded) => {
        if (controller.signal.aborted) return;
        setDiagnostics(loaded);
        setIsLoading(false);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setIsLoading(false);
        setLoadFailed(true);
        console.error('Failed to load diagnostics', error);
      });
    return () => controller.abort();
  }, [enabled, requestID]);

  const refresh = () => setRequestID((id) => id + 1);
  const waitingToLoad = enabled && diagnostics === null && !loadFailed;
  return { diagnostics, isLoading: enabled && (isLoading || waitingToLoad), loadFailed, refresh };
}
