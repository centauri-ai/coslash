import { expect, it, vi } from 'vitest';
import { apiFetch } from '@/pages/coslash/lib/api';
import { loadSynthesisCosts } from './use-synthesis-costs';

vi.mock('@/pages/coslash/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/pages/coslash/lib/api')>()),
  apiFetch: vi.fn(),
}));

it('rejects HTTP and malformed responses instead of treating them as zero spend', async () => {
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response('{}', { status: 503 }));
  await expect(loadSynthesisCosts({ since: 1, until: 2 }, new AbortController().signal)).rejects.toThrow(
    '503',
  );
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response('{}'));
  await expect(loadSynthesisCosts({ since: 1, until: 2 }, new AbortController().signal)).rejects.toThrow(
    'Invalid synthesis costs response',
  );
});

it('passes abort signals to the request so a superseded month can be cancelled', async () => {
  const first = new AbortController();
  vi.mocked(apiFetch).mockImplementationOnce((_path, init) => {
    expect(init?.signal).toBe(first.signal);
    return new Promise((_resolve, reject) => {
      first.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    });
  });
  const stale = loadSynthesisCosts({ since: 1, until: 2 }, first.signal);
  first.abort();
  await expect(stale).rejects.toMatchObject({ name: 'AbortError' });
});
