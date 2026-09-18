import { useEffect, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import type { Diagnostics } from '@/pages/coslash/lib/diagnostics';

export function useDiagnostics(enabled: boolean) {
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [requestID, setRequestID] = useState(0);
  const [completedRequestID, setCompletedRequestID] = useState(-1);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    apiFetch('/api/diagnostics', { signal: controller.signal })
      .then((response) => {
        if (!response.ok) throw new Error(`Diagnostics request failed (${response.status})`);
        return response.json() as Promise<Diagnostics>;
      })
      .then((loaded) => {
        if (controller.signal.aborted) return;
        setDiagnostics(loaded);
        setLoadFailed(false);
        setCompletedRequestID(requestID);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setLoadFailed(true);
        setCompletedRequestID(requestID);
        console.error('Failed to load diagnostics', error);
      });
    return () => controller.abort();
  }, [enabled, requestID]);

  const refresh = () => setRequestID((id) => id + 1);
  return {
    diagnostics,
    isLoading: enabled && completedRequestID !== requestID,
    loadFailed,
    refresh,
  };
}
