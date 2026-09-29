import { useCallback, useEffect, useRef, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';

export function useDirectedHandoffs() {
  const [handoffs, setHandoffs] = useState<DirectedHandoff[]>([]);
  const [error, setError] = useState<string | null>(null);
  const requestId = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++requestId.current;
    try {
      const response = await apiFetch('/api/directed-handoffs');
      if (!response.ok) throw new Error(`Could not load handoffs (${response.status})`);
      const body = (await response.json()) as { handoffs: DirectedHandoff[] };
      if (current === requestId.current) {
        setHandoffs(body.handoffs);
        setError(null);
      }
    } catch (failure) {
      if (current === requestId.current)
        setError(failure instanceof Error ? failure.message : String(failure));
    }
  }, []);

  /* oxlint-disable react/set-state-in-effect -- load and poll backend handoff state */
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), 3000);
    return () => window.clearInterval(timer);
  }, [refresh]);
  /* oxlint-enable react/set-state-in-effect */

  return { handoffs, error, refresh };
}
