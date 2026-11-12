// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { HubConnectionsSection } from './HubConnectionsSection';

const primary = { origin: 'https://primary.example', primary: true, enabled: true, state: 'connected_idle' };
const secondary = { origin: 'https://secondary.example', primary: false, enabled: false, state: 'disabled' };
const started = (id: string, verificationUri = `${secondary.origin}/approve`) =>
  response({ id: id.repeat(32), pairing: { userCode: 'ABCD', verificationUri } }, 201);
const response = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });

type Handler = (path: string, body: unknown) => Response | Promise<Response> | undefined;
function stubApi(hubs: () => unknown, handler: Handler = () => undefined) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === '/api/hubs/status') return response(hubs());
      const result = handler(path, init?.body ? JSON.parse(String(init.body)) : undefined);
      if (!result) throw new Error(`Unexpected ${path}`);
      return result;
    }),
  );
}

function button(text: string) {
  const found = [...document.querySelectorAll('button')].find((item) => item.textContent?.trim() === text);
  if (!found) throw new Error(`Missing button ${text}`);
  return found;
}
async function click(target: HTMLElement) {
  await act(async () => {
    target.click();
  });
  await settle();
}
async function settle() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}
async function setOrigin(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[aria-label="Hub origin"]');
  if (!input) throw new Error('Missing origin input');
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
function consent() {
  const item = document.querySelector<HTMLInputElement>('input[type="checkbox"]');
  if (!item) throw new Error('Missing consent checkbox');
  return item;
}
const alertText = () => document.querySelector('[role="alert"]')?.textContent;

let unmount: (() => Promise<void>) | undefined;
beforeEach(() => {
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});
async function renderConnections() {
  const mount = document.createElement('div');
  document.body.append(mount);
  const root = createRoot(mount);
  unmount = async () => {
    await act(async () => root.unmount());
    unmount = undefined;
  };
  await act(async () => root.render(<HubConnectionsSection />));
  await settle();
}
afterEach(async () => {
  await unmount?.();
  vi.unstubAllGlobals();
  document.body.replaceChildren();
});

it('pairs with consent, then enables with separate consent and manages the secondary', async () => {
  let hubs = [primary];
  const polls = ['pending', 'declined', 'paired_disabled'];
  const calls: string[] = [];
  stubApi(
    () => hubs,
    (path, body) => {
      calls.push(path);
      if (path === '/api/hubs/pairings') {
        expect(body).toEqual({ origin: secondary.origin, consent: true });
        return started('a');
      }
      if (path.endsWith('/poll')) {
        const state = polls.shift();
        if (state === 'paired_disabled') hubs = [primary, secondary];
        return response({ state });
      }
      if (path === '/api/hubs/enable') {
        expect(body).toEqual({ origin: secondary.origin, consent: true });
        hubs = [primary, { ...secondary, enabled: true, state: 'connected_idle' }];
        return response({ state: 'started' });
      }
      expect(body).toEqual({ origin: secondary.origin });
      if (path === '/api/hubs/disable') hubs = [primary, secondary];
      return response({ state: 'ok' });
    },
  );
  await renderConnections();
  expect(document.body.textContent).toContain('https://primary.example (Primary)');

  await click(button('Add Hub'));
  expect(document.body.textContent).toContain('does not authorize or start personal sync');
  await setOrigin('https://secondary.example/path');
  expect(alertText()).toContain('Enter an HTTPS Hub origin');
  expect(consent().disabled).toBe(true);
  await setOrigin(secondary.origin);
  await click(consent());
  await setOrigin('https://other.example');
  expect(consent().checked).toBe(false);
  expect(button('Start pairing').disabled).toBe(true);
  await setOrigin(secondary.origin);
  await click(consent());
  await click(button('Start pairing'));
  expect(document.querySelector('a')?.getAttribute('href')).toBe(`${secondary.origin}/approve`);
  await click(button('Check approval'));
  expect(document.body.textContent).toContain('Waiting for approval');
  await click(button('Check approval'));
  expect(alertText()).toContain('declined');

  await click(button('Close'));
  await click(button('Add Hub'));
  await setOrigin(secondary.origin);
  await click(consent());
  await click(button('Start pairing'));
  await click(button('Check approval'));
  expect(document.body.textContent).toContain('Paired. Sync is disabled');
  expect(calls).not.toContain('/api/hubs/enable');

  await click(button('Close'));
  await click(button('Enable'));
  expect(document.body.textContent).toContain('prompts, commands and source snippets');
  expect(button('Enable sync').disabled).toBe(true);
  await click(consent());
  await click(button('Enable sync'));
  expect(document.body.textContent).toContain('Sync started');
  await click(button('Retry'));
  await click(button('Disable'));
  expect(calls.slice(-3)).toEqual(['/api/hubs/enable', '/api/hubs/retry', '/api/hubs/disable']);
  expect(document.querySelector('[aria-label="Enable https://secondary.example"]')).not.toBeNull();
});

it('pairs a known Hub again and ignores a completion from a closed flow', async () => {
  let resolvePoll: ((value: Response) => void) | undefined;
  stubApi(
    () => [primary, { ...secondary, enabled: true, state: 'not_connected' }],
    (path, body) => {
      if (path === '/api/hubs/pairings') {
        expect(body).toEqual({ origin: secondary.origin, consent: true });
        return started('b');
      }
      if (path.endsWith('/poll'))
        return new Promise<Response>((resolve) => {
          resolvePoll = resolve;
        });
    },
  );
  await renderConnections();
  await click(button('Pair again'));
  expect(document.body.textContent).toContain('Uploads to this Hub then start over');
  expect(document.querySelector('input[aria-label="Hub origin"]')).toBeNull();
  await click(consent());
  await click(button('Start pairing'));
  await click(button('Check approval'));
  await click(button('Close'));
  await act(async () => {
    resolvePoll?.(response({ state: 'paired_disabled' }));
    await Promise.resolve();
  });
  expect(document.body.textContent).not.toContain('Paired.');
  expect(button('Add Hub').disabled).toBe(false);
});

it('clears consent when switching flows', async () => {
  stubApi(() => [primary, secondary]);
  await renderConnections();
  await click(button('Enable'));
  await click(consent());
  expect(button('Enable sync').disabled).toBe(false);
  await click(button('Pair again'));
  expect(consent().checked).toBe(false);
  expect(button('Start pairing').disabled).toBe(true);
});

it('shows a status load failure and retries it', async () => {
  let fail = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => (fail ? new Response('Hub status unavailable', { status: 503 }) : response([primary]))),
  );
  await renderConnections();
  expect(alertText()).toContain('Hub status unavailable');
  expect(button('Add Hub').disabled).toBe(true);
  fail = false;
  await click(button('Retry loading'));
  expect(button('Add Hub').disabled).toBe(false);
});

it('surfaces an enable failure and keeps the flow open for retry', async () => {
  stubApi(
    () => [primary, secondary],
    () => new Response('This Hub could not be enabled.', { status: 409 }),
  );
  await renderConnections();
  await click(button('Enable'));
  await click(consent());
  await click(button('Enable sync'));
  expect(alertText()).toContain('This Hub could not be enabled.');
  expect(button('Enable sync').disabled).toBe(false);
});

it('does not offer an approval link to a different origin', async () => {
  stubApi(
    () => [primary],
    () => started('d', 'https://other.example/approve'),
  );
  await renderConnections();
  await click(button('Add Hub'));
  await setOrigin(secondary.origin);
  await click(consent());
  await click(button('Start pairing'));
  expect(alertText()).toContain('Invalid Hub approval link');
  expect(document.querySelector('a')).toBeNull();
});

it('shows an origin-scoped connection failure instead of claiming the Hub is connected', async () => {
  const failure = 'Hub is temporarily unavailable. Try again shortly.';
  stubApi(() => [primary, { ...secondary, enabled: true, state: 'connected_idle', failure }]);
  await renderConnections();
  expect(alertText()).toContain(failure);
  expect(document.body.textContent).toContain('Connection issue');
  expect(document.querySelector('[aria-label="Retry https://secondary.example"]')).not.toBeNull();
});
