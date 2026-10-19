export type LocalUpdate = { available: boolean; required: boolean; version?: string; downloadUrl?: string };

export function isLocalUpdate(value: unknown): value is LocalUpdate {
  if (value == null || typeof value !== 'object') return false;
  const prompt = value as Record<string, unknown>;
  if (typeof prompt.downloadUrl === 'string') {
    try {
      const parsed = new URL(prompt.downloadUrl);
      if (parsed.protocol !== 'https:' || parsed.username || parsed.password) return false;
    } catch {
      return false;
    }
  }
  return (
    typeof prompt.available === 'boolean' &&
    typeof prompt.required === 'boolean' &&
    (prompt.version == null || typeof prompt.version === 'string') &&
    (prompt.downloadUrl == null || typeof prompt.downloadUrl === 'string')
  );
}
