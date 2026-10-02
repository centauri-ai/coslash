import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { launchFreshSession } from '@/pages/coslash/hooks/use-launch-terminal';
import { apiFetch } from '@/pages/coslash/lib/api';
import { cursorHandoffText } from '@/pages/coslash/lib/handoff';

vi.mock('@/pages/coslash/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/pages/coslash/lib/api')>()),
  apiFetch: vi.fn(),
}));

const source = { sourceId: 'local', agent: 'codex', id: 'origin', entrypoint: 'cli' };
const writeText = vi.fn();

beforeEach(() => {
  vi.mocked(apiFetch).mockResolvedValue(new Response(null));
  writeText.mockResolvedValue(undefined);
  vi.stubGlobal('navigator', { clipboard: { writeText } });
});

afterEach(() => {
  vi.resetAllMocks();
  vi.unstubAllGlobals();
});

it('starts the original agent with only the handoff, without target discovery or a task', async () => {
  await launchFreshSession(source, 'prior context');
  expect(writeText).toHaveBeenCalledWith('prior context');
  expect(apiFetch).toHaveBeenCalledExactlyOnceWith(
    '/api/launch?source=local&agent=codex&id=origin&mode=new',
    { method: 'POST', body: 'prior context' },
  );
});

it.each(['cursor-ide', 'cursor-cli'])(
  'preserves the %s entrypoint and copies its guarded notes',
  async (entrypoint) => {
    await launchFreshSession({ ...source, agent: 'cursor', entrypoint }, 'prior context');
    expect(writeText).toHaveBeenCalledWith(cursorHandoffText('prior context'));
    expect(apiFetch).toHaveBeenCalledExactlyOnceWith(
      `/api/launch?source=local&agent=cursor&id=origin&mode=${entrypoint === 'cursor-ide' ? 'open' : 'new'}`,
      { method: 'POST', body: undefined },
    );
  },
);

it('prevents Cursor from opening when the required clipboard copy fails', async () => {
  writeText.mockRejectedValue(new Error('denied'));
  await expect(
    launchFreshSession({ ...source, agent: 'cursor', entrypoint: 'cursor-ide' }, 'notes'),
  ).rejects.toThrow('Could not copy the handoff');
  expect(apiFetch).not.toHaveBeenCalled();
});

it('keeps a remote fresh launch usable without clipboard access and allows retry after failure', async () => {
  writeText.mockRejectedValue(new Error('denied'));
  const remote = { ...source, sourceId: 'r_0123456789abcdef' };
  vi.mocked(apiFetch).mockResolvedValueOnce(new Response(null, { status: 500 }));
  await expect(launchFreshSession(remote, 'notes')).rejects.toThrow('Launch failed (500)');
  await launchFreshSession(remote, 'notes');
  expect(apiFetch).toHaveBeenLastCalledWith(
    '/api/launch?source=r_0123456789abcdef&agent=codex&id=origin&mode=new',
    { method: 'POST', body: 'notes' },
  );
});

it('preserves the actionable Pi launch explanation and allows retry', async () => {
  const pi = { ...source, agent: 'pi', entrypoint: 'pi-tui' };
  vi.mocked(apiFetch).mockResolvedValueOnce(
    new Response(
      JSON.stringify({
        code: 'pi_runtime_unsupported',
        error: 'Pi launch requires a stable release at least 0.99.1',
      }),
      { status: 409, headers: { 'Content-Type': 'application/json' } },
    ),
  );
  await expect(launchFreshSession(pi, 'notes')).rejects.toThrow(
    'Pi launch requires a stable release at least 0.99.1',
  );
  await expect(launchFreshSession(pi, 'notes')).resolves.toBeUndefined();
});
