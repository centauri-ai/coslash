// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import { remoteStatus } from '@/pages/coslash/lib/remote-api';
import { MachinesSettingsSection } from './MachinesSettingsSection';

vi.mock('@/pages/coslash/lib/remote-api', () => ({ remoteStatus: vi.fn() }));

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('keeps checking the SSH status after a failed request', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.mocked(remoteStatus).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({
    sourceId: 'r_0123456789abcdef',
    label: 'test-host',
    state: 'disabled',
    complete: true,
  });
  const mount = document.createElement('div');
  const root = createRoot(mount);
  await act(async () =>
    root.render(
      <MachinesSettingsSection
        remote={{ sshAlias: 'test-host', enabled: true }}
        onAddHost={async () => true}
        onRemoveHost={async () => true}
        onBusyChange={() => {}}
      />,
    ),
  );
  expect(mount.textContent).toContain('Checking the connection.');
  await act(async () => vi.advanceTimersByTimeAsync(2000));
  expect(remoteStatus).toHaveBeenCalledTimes(2);
  expect(mount.textContent).toContain('Remote collection is disabled.');
  expect(mount.textContent).not.toContain('Checking the connection.');
  await act(async () => root.unmount());
});
