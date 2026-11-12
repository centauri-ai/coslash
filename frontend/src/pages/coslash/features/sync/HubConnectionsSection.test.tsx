// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import { HubConnectionsSection } from './HubConnectionsSection';

const primary = {
  origin: 'https://primary.example',
  primary: true,
  enabled: true,
  state: 'connected_idle',
  quarantined: 0,
};
const secondary = {
  origin: 'https://secondary.example',
  primary: false,
  enabled: false,
  state: 'disabled',
  quarantined: 0,
};
const disclosure = 'This disclosure applies to the named Hub.';
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

function button(text: string) {
  const found = [...document.querySelectorAll('button')].find((item) => item.textContent?.trim() === text);
  if (!found) throw new Error(`Missing button ${text}`);
  return found;
}
async function click(target: HTMLElement) {
  await act(async () => {
    target.click();
  });
}
async function settle() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}
function setOrigin(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[aria-label="Hub origin"]');
  if (!input) throw new Error('Missing origin input');
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event('input', { bubbles: true }));
}
function checkbox(index: number) {
  const item = document.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')[index];
  if (!item) throw new Error(`Missing checkbox ${index}`);
  return item;
}

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.replaceChildren();
});

it('requires separate consent, shows pending and declined states, and manages each secondary', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let hubs = [primary];
  let poll = 0;
  let starts = 0;
  const actions: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/hubs/status') return response(hubs);
    if (path.endsWith('-disclosure')) return response({ disclosure });
    if (path === '/api/hubs/pairings') {
      starts += 1;
      const body = JSON.parse(String(init?.body)) as { origin: string; consent: boolean };
      expect(body).toEqual({ origin: secondary.origin, consent: true });
      return response(
        { id: 'a'.repeat(32), pairing: { userCode: 'ABCD', verificationUri: `${secondary.origin}/approve` } },
        201,
      );
    }
    if (path.endsWith('/poll')) {
      poll += 1;
      const state = poll === 1 ? 'pending' : poll === 2 ? 'declined' : 'paired_disabled';
      if (state === 'paired_disabled') hubs = [primary, secondary];
      return response({ state });
    }
    if (path === '/api/hubs/enable') {
      actions.push('enable');
      expect(JSON.parse(String(init?.body))).toEqual({ origin: secondary.origin, consent: true });
      hubs = [primary, { ...secondary, enabled: true, state: 'connected_idle' }];
      return response({ state: 'started' });
    }
    if (path === '/api/hubs/retry' || path === '/api/hubs/disable') {
      actions.push(path);
      expect(JSON.parse(String(init?.body))).toEqual({ origin: secondary.origin });
      if (path.endsWith('disable')) hubs = [primary, secondary];
      return response({ state: 'ok' });
    }
    throw new Error(`Unexpected ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  expect(document.body.textContent).toContain('https://primary.example');
  expect(document.body.textContent).toContain('(Primary)');
  await click(button('Add Hub'));
  await settle();
  await act(async () => {
    setOrigin(secondary.origin);
  });
  expect(button('Start pairing').disabled).toBe(true);
  expect(starts).toBe(0);
  await click(checkbox(0));
  await act(async () => {
    setOrigin('https://other.example');
  });
  expect(checkbox(0).checked).toBe(false);
  expect(button('Start pairing').disabled).toBe(true);
  await act(async () => {
    setOrigin(secondary.origin);
  });
  await click(checkbox(0));
  await click(button('Start pairing'));
  await settle();
  expect(document.body.textContent).toContain('Waiting for approval');
  expect(document.querySelector('a')?.getAttribute('href')).toBe(`${secondary.origin}/approve`);
  await click(button('Check approval'));
  await settle();
  expect(document.body.textContent).toContain('Waiting for approval');
  await click(button('Check approval'));
  await settle();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('declined');
  await click(button('Close'));
  await click(button('Add Hub'));
  await settle();
  await act(async () => {
    setOrigin(secondary.origin);
  });
  await click(checkbox(0));
  await click(button('Start pairing'));
  await settle();
  await click(button('Check approval'));
  await settle();
  expect(document.body.textContent).toContain('Paired. Sync is disabled');
  expect(actions).toEqual([]);
  await click(button('Close'));
  await click(button('Enable'));
  await settle();
  expect(button('Enable sync').disabled).toBe(true);
  await click(checkbox(0));
  await click(button('Enable sync'));
  await settle();
  expect(actions).toEqual(['enable']);
  expect(document.body.textContent).toContain('Sync started');
  await click(button('Retry'));
  await settle();
  await click(button('Disable'));
  await settle();
  expect(actions).toEqual(['enable', '/api/hubs/retry', '/api/hubs/disable']);
  expect(document.body.textContent).toContain('Sync disabled');
  expect(document.querySelector('[aria-label="Enable https://secondary.example"]')).not.toBeNull();
  await act(async () => root.unmount());
  mount.remove();
});

it('requires explicit new-binding consent, preserves errors and ignores closed attempts', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let resolvePoll: ((value: Response) => void) | undefined;
  let pollSignal: AbortSignal | null | undefined;
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path === '/api/hubs/status') return response([primary, { ...secondary, state: 'disconnected' }]);
    if (path === '/api/hubs/recovery-disclosure')
      return response({
        disclosure: 'Old pending data stays on this computer and will not be sent to the new account.',
      });
    if (path === '/api/hubs/re-pairings') {
      expect(JSON.parse(String(init?.body))).toEqual({
        origin: secondary.origin,
        consent: true,
        newBindingConsent: true,
      });
      return response(
        { id: 'b'.repeat(32), pairing: { userCode: 'EFGH', verificationUri: `${secondary.origin}/approve` } },
        201,
      );
    }
    if (path.endsWith('/poll')) {
      pollSignal = init?.signal;
      return new Promise<Response>((resolve) => {
        resolvePoll = resolve;
      });
    }
    throw new Error(`Unexpected ${path}`);
  });
  vi.stubGlobal('fetch', fetchMock);
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  await click(button('Recover pairing'));
  await settle();
  expect(document.body.textContent).toContain('will not be sent to the new account');
  expect(button('Start recovery').disabled).toBe(true);
  await click(checkbox(0));
  await click(checkbox(1));
  await click(button('Start recovery'));
  await settle();
  await click(button('Check approval'));
  await act(async () => root.unmount());
  mount.remove();
  expect(pollSignal?.aborted).toBe(true);
  await act(async () => {
    resolvePoll?.(response({ state: 'recovered_new_binding_disabled' }));
    await Promise.resolve();
  });
  expect(fetchMock.mock.calls.filter(([path]) => String(path) === '/api/hubs/enable')).toHaveLength(0);
});

it('shows server errors and allows status loading to be retried', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let fail = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) !== '/api/hubs/status') throw new Error('unexpected request');
      if (fail) return new Response('Hub status unavailable', { status: 503 });
      return response([primary]);
    }),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('Hub status unavailable');
  expect(button('Add Hub').disabled).toBe(true);
  fail = false;
  await click(button('Retry loading'));
  await settle();
  expect(button('Add Hub').disabled).toBe(false);
  await act(async () => root.unmount());
  mount.remove();
});

it('shows new-binding recovery as disabled and surfaces an enable failure', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let quarantined = 0;
  let enableCalls = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === '/api/hubs/status')
        return response([primary, { ...secondary, state: 'disconnected', quarantined }]);
      if (path.endsWith('-disclosure'))
        return response({
          disclosure:
            'Old pending data stays on this computer; eligible sessions may upload after you enable sync.',
        });
      if (path === '/api/hubs/re-pairings') {
        expect(JSON.parse(String(init?.body))).toEqual({
          origin: secondary.origin,
          consent: true,
          newBindingConsent: true,
        });
        return response(
          {
            id: 'c'.repeat(32),
            pairing: { userCode: 'CODE', verificationUri: `${secondary.origin}/approve` },
          },
          201,
        );
      }
      if (path.endsWith('/poll')) {
        quarantined = 1;
        return response({ state: 'recovered_new_binding_disabled' });
      }
      if (path === '/api/hubs/enable') {
        enableCalls += 1;
        return new Response('secondary Hub could not be enabled', { status: 409 });
      }
      throw new Error(`Unexpected ${path}`);
    }),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  await click(button('Recover pairing'));
  await settle();
  await click(checkbox(0));
  await click(checkbox(1));
  await click(button('Start recovery'));
  await settle();
  await click(button('Check approval'));
  await settle();
  expect(document.body.textContent).toContain('Old pending data stays on this computer; sync is disabled');
  expect(document.body.textContent).toContain('Old pending data kept on this computer (1)');
  expect(enableCalls).toBe(0);
  await click(button('Close'));
  await click(button('Enable'));
  await settle();
  await click(checkbox(0));
  await click(button('Enable sync'));
  await settle();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain(
    'secondary Hub could not be enabled',
  );
  expect(enableCalls).toBe(1);
  expect(button('Enable sync')).not.toBeNull();
  await act(async () => root.unmount());
  mount.remove();
});

it('does not offer an approval link to a different origin', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === '/api/hubs/status') return response([primary]);
      if (path === '/api/hubs/pairing-disclosure') return response({ disclosure });
      if (path === '/api/hubs/pairings')
        return response(
          {
            id: 'd'.repeat(32),
            pairing: { userCode: 'CODE', verificationUri: 'https://other.example/approve' },
          },
          201,
        );
      throw new Error(`Unexpected ${path}`);
    }),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  await click(button('Add Hub'));
  await settle();
  await act(async () => {
    setOrigin(secondary.origin);
  });
  await click(checkbox(0));
  await click(button('Start pairing'));
  await settle();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('Invalid Hub approval link');
  expect(document.querySelector('a')).toBeNull();
  await act(async () => root.unmount());
  mount.remove();
});

it('shows an origin-scoped connection failure instead of claiming the Hub is connected', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      response([
        primary,
        {
          ...secondary,
          enabled: true,
          state: 'connected_idle',
          failure: 'Hub is temporarily unavailable. Try again shortly.',
        },
      ]),
    ),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => {
    root.render(<HubConnectionsSection />);
  });
  await settle();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain('Hub is temporarily unavailable');
  expect(document.body.textContent).toContain('Connection issue');
  expect(document.querySelector('[aria-label="Retry https://secondary.example"]')).not.toBeNull();
  await act(async () => root.unmount());
  mount.remove();
});

it('clears the old disclosure when changing flows and ignores its late response', async () => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let resolveRecovery: ((value: Response) => void) | undefined;
  let resolveEnable: ((value: Response) => void) | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input);
      if (path === '/api/hubs/status') return response([primary, secondary]);
      if (path === '/api/hubs/recovery-disclosure')
        return new Promise<Response>((resolve) => {
          resolveRecovery = resolve;
        });
      if (path === '/api/hubs/enable-disclosure')
        return new Promise<Response>((resolve) => {
          resolveEnable = resolve;
        });
      throw new Error(`Unexpected ${path}`);
    }),
  );
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  await act(async () => root.render(<HubConnectionsSection />));
  await settle();
  await click(button('Recover pairing'));
  await click(button('Enable'));
  expect(button('Enable sync').disabled).toBe(true);
  await act(async () => {
    resolveRecovery?.(response({ disclosure: 'Old recovery disclosure' }));
  });
  expect(document.body.textContent).not.toContain('Old recovery disclosure');
  await act(async () => {
    resolveEnable?.(response({ disclosure: 'Enable disclosure' }));
  });
  expect(document.body.textContent).toContain('Enable disclosure');
  await click(checkbox(0));
  expect(button('Enable sync').disabled).toBe(false);
  await click(button('Recover pairing'));
  expect(document.body.textContent).not.toContain('Enable disclosure');
  expect(button('Start recovery').disabled).toBe(true);
  await act(async () => root.unmount());
  mount.remove();
});
