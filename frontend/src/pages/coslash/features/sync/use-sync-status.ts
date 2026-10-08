import { useEffect, useRef, useState } from 'react';
import { loadSyncStatus, type SyncStatus, type SyncStatusLoad } from './api';

const POLL_INTERVAL_MS = 10_000;

function sameStatus(left: SyncStatus, right: SyncStatus): boolean {
  const leftKeys = Object.keys(left.sessions);
  const rightKeys = Object.keys(right.sessions);
  return (
    left.state === right.state &&
    left.hubOrigin === right.hubOrigin &&
    leftKeys.length === rightKeys.length &&
    leftKeys.every((key) => left.sessions[key] === right.sessions[key])
  );
}

export function useSyncStatus() {
  const [status, setStatus] = useState<SyncStatus>({ state: 'not_connected', hubOrigin: '', sessions: {} });
  const [loadState, setLoadState] = useState<SyncStatusLoad>('loading');
  const [loadError, setLoadError] = useState<string | null>(null);
  const [hasStatus, setHasStatus] = useState(false);
  const statusRef = useRef(status);

  useEffect(() => {
    let live = true;
    let sequence = 0;
    let activeRequest: AbortController | null = null;
    const refresh = () => {
      if (document.visibilityState === 'hidden') return;
      activeRequest?.abort();
      const attempt = ++sequence;
      const request = new AbortController();
      activeRequest = request;
      void loadSyncStatus(request.signal).then(
        (next) => {
          if (!live || attempt !== sequence) return;
          if (!sameStatus(statusRef.current, next)) {
            statusRef.current = next;
            setStatus(next);
          }
          setLoadError(null);
          setLoadState('ready');
          setHasStatus(true);
        },
        (error: unknown) => {
          if (!live || request.signal.aborted || attempt !== sequence) return;
          setLoadError(error instanceof Error ? error.message : 'Sync status is unavailable');
          setLoadState('error');
        },
      );
    };
    refresh();
    const interval = window.setInterval(refresh, POLL_INTERVAL_MS);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      live = false;
      sequence += 1;
      activeRequest?.abort();
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, []);

  return { status, loadState, loadError, hasStatus };
}
