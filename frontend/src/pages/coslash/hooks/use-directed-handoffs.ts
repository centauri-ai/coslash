import { useCallback, useEffect, useRef, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import type { DirectedHandoff } from '@/pages/coslash/lib/directed-handoff';

export function useDirectedHandoffs() {
  const [handoffs, setHandoffs] = useState<DirectedHandoff[]>([]);
  const [error, setError] = useState<string | null>(null);
  const requestId = useRef(0);
  const inFlight = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++requestId.current;
    inFlight.current += 1;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 10_000);
    try {
      const response = await apiFetch('/api/directed-handoffs', { signal: controller.signal });
      if (!response.ok) throw new Error(`Could not load handoffs (${response.status})`);
      const body = (await response.json()) as { handoffs: DirectedHandoff[] };
      if (current === requestId.current) {
        setHandoffs(body.handoffs);
        setError(null);
      }
    } catch (failure) {
      if (current !== requestId.current) return;
      if (controller.signal.aborted) setError('Could not load handoffs (timed out)');
      else setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      clearTimeout(timeout);
      inFlight.current -= 1;
    }
  }, []);

  /* oxlint-disable react/set-state-in-effect -- load and poll backend handoff state */
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => {
      if (inFlight.current === 0) void refresh();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [refresh]);
  /* oxlint-enable react/set-state-in-effect */

  return { handoffs, error, refresh };
}
