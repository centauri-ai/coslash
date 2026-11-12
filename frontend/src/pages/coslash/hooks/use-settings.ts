import { useEffect, useRef, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  decodeSettingsResponse,
  settingsForSave,
  type CoslashSettings,
  type RemoteOwnershipAction,
  type SettingsResponse,
} from '@/pages/coslash/lib/settings';

async function readError(response: Response): Promise<string> {
  const message = (await response.text()).trim();
  return message || `Settings request failed (${response.status})`;
}

export function useSettings() {
  const [response, setResponse] = useState<SettingsResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  // Saves run one at a time, so a queued save builds on the newest response.
  const responseRef = useRef<SettingsResponse | null>(null);
  const saveQueue = useRef<Promise<unknown>>(Promise.resolve());
  const pendingSaves = useRef(0);
  const saveAttempt = useRef(0);

  const commitResponse = (next: SettingsResponse | null) => {
    responseRef.current = next;
    setResponse(next);
  };

  useEffect(() => {
    const controller = new AbortController();
    apiFetch('/api/settings', { signal: controller.signal })
      .then(async (result) => {
        if (!result.ok) throw new Error(await readError(result));
        return decodeSettingsResponse(await result.json());
      })
      .then((loaded) => {
        if (controller.signal.aborted) return;
        commitResponse(loaded);
        setIsLoading(false);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setLoadError(error instanceof Error ? error.message : String(error));
        setIsLoading(false);
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!response?.options.refreshing) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const result = await apiFetch('/api/settings', { signal: controller.signal });
        if (!result.ok) throw new Error(await readError(result));
        const loaded = decodeSettingsResponse(await result.json());
        if (controller.signal.aborted) return;
        // A discovery response must never overwrite a save completed in the meantime.
        commitResponse(responseRef.current && { ...responseRef.current, options: loaded.options });
        if (!loaded.options.refreshing) return;
      } catch {
        if (controller.signal.aborted) return;
      }
      timer = setTimeout(refresh, 1000);
    };
    timer = setTimeout(refresh, 1000);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [response?.options.refreshing]);

  const save = (
    next: CoslashSettings | ((current: SettingsResponse) => CoslashSettings),
    remoteOwnershipAction?: RemoteOwnershipAction,
  ): Promise<boolean> => {
    const attempt = ++saveAttempt.current;
    pendingSaves.current += 1;
    setIsSaving(true);
    setSaveError(null);
    const run = async () => {
      try {
        const current = responseRef.current;
        if (!current) throw new Error('Settings are not loaded.');
        const settings = typeof next === 'function' ? next(current) : next;
        const result = await apiFetch('/api/settings', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ settings: settingsForSave(settings), remoteOwnershipAction }),
        });
        if (!result.ok) throw new Error(await readError(result));
        commitResponse(decodeSettingsResponse(await result.json()));
        return true;
      } catch (error: unknown) {
        if (attempt === saveAttempt.current)
          setSaveError(error instanceof Error ? error.message : String(error));
        return false;
      } finally {
        pendingSaves.current -= 1;
        if (pendingSaves.current === 0) setIsSaving(false);
      }
    };
    const result = saveQueue.current.then(run);
    saveQueue.current = result;
    return result;
  };

  return { response, isLoading, loadError, saveError, isSaving, save };
}
