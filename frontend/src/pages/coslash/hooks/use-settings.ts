import { useEffect, useState } from 'react';
import { apiFetch } from '@/pages/coslash/lib/api';
import {
  decodeSettingsResponse,
  type CoslashSettings,
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

  useEffect(() => {
    const controller = new AbortController();
    apiFetch('/api/settings', { signal: controller.signal })
      .then(async (result) => {
        if (!result.ok) throw new Error(await readError(result));
        return decodeSettingsResponse(await result.json());
      })
      .then((loaded) => {
        if (controller.signal.aborted) return;
        setResponse(loaded);
        setIsLoading(false);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setLoadError(error instanceof Error ? error.message : String(error));
        setIsLoading(false);
      });
    return () => controller.abort();
  }, []);

  const save = async (settings: CoslashSettings): Promise<boolean> => {
    setIsSaving(true);
    setSaveError(null);
    try {
      const result = await apiFetch('/api/settings', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settings),
      });
      if (!result.ok) throw new Error(await readError(result));
      setResponse(decodeSettingsResponse(await result.json()));
      setIsSaving(false);
      return true;
    } catch (error: unknown) {
      setSaveError(error instanceof Error ? error.message : String(error));
      setIsSaving(false);
      return false;
    }
  };

  return { response, isLoading, loadError, saveError, isSaving, save };
}
