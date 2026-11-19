import { createRequire } from 'node:module';
import { expect, test } from '@playwright/test';
import { checkLocal, openLocal, step } from './helpers.mjs';

test.beforeEach(async ({ request }) => {
  await checkLocal(request);
});

test('search and agent filter change the list', async ({ page }) => {
  await openLocal(page);
  await Promise.all([
    page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname === '/api/sessions' &&
        !url.searchParams.has('since') &&
        !url.searchParams.has('remoteSince')
      );
    }),
    page.getByRole('button', { name: 'All time' }).click(),
  ]);
  await expect(page.getByText('Refreshing sessions', { exact: true })).toBeHidden();
  const total = page.getByRole('strong').filter({ hasText: /^\d+ sessions?$/ });
  const count = async () => Number.parseInt((await total.textContent()) ?? '', 10);
  await expect(total).toBeVisible();
  const before = await count();
  expect(before).toBeGreaterThan(1);
  await step(page, 'before-search');

  const search = page.getByRole('searchbox', { name: /Search session/ });
  await search.fill('scheduler');
  await expect.poll(count).toBeLessThan(before);
  const after = await count();
  expect(after).toBeGreaterThan(0);
  const titles = page.getByRole('table').getByRole('button', { name: /^Synthetic: / });
  await expect(titles.first()).toBeVisible();
  for (const title of await titles.allTextContents()) expect(title.toLowerCase()).toContain('scheduler');
  await step(page, 'searched-scheduler');

  await search.fill('zzz-no-such-session');
  await expect(page.getByText(/^0 sessions?$/)).toBeVisible();
  await step(page, 'search-no-match');

  await search.fill('');
  await expect.poll(count).toBe(before);

  await page.getByRole('region', { name: 'Filters' }).getByRole('button', { name: 'Codex' }).click();
  await expect.poll(count).toBeLessThan(before);
  await step(page, 'filtered-codex');
});

test('ranked search loads older matches, opens evidence, and fits a narrow screen', async ({
  page,
  request,
}) => {
  const headers = { 'X-Coslash-Token': process.env.COSLASH_LOCAL_TOKEN };
  const { sessions: source } = await (await request.get('/api/sessions?sourceAware=1', { headers })).json();
  const original = source.find((session) => session.sourceId === 'local' && session.agent === 'codex');
  expect(original).toBeTruthy();
  const older = { ...original, name: 'Historical queue decision', mtime: 1 };
  let allTimeRequested = false;
  let releaseList;
  const listGate = new Promise((resolve) => {
    releaseList = resolve;
  });
  await page.route('**/api/sessions?*', async (route) => {
    const url = new URL(route.request().url());
    if (url.searchParams.has('since') || url.searchParams.has('remoteSince')) {
      return route.fulfill({ json: [source.find((session) => session.id !== original.id)] });
    }
    allTimeRequested = true;
    await listGate;
    return route.fulfill({ json: [older] });
  });
  await page.route('**/api/sessions', async (route) => {
    allTimeRequested = true;
    await listGate;
    return route.fulfill({ json: [older] });
  });
  let ready = false;
  await page.route('**/api/search/status', (route) =>
    route.fulfill({ json: { state: ready ? 'ready' : 'building', indexed: ready ? 1 : 0, total: 1 } }),
  );
  const identity = { sourceId: older.sourceId, agent: older.agent, sessionId: older.id };
  await page.route('**/api/search?*', (route) =>
    route.fulfill({
      json: {
        indexState: 'ready',
        truncated: false,
        indexed: [],
        sessions: [
          {
            ...identity,
            score: 9,
            coverage: 'full',
            match: { kind: 'prompt', turn: 12, snippet: 'Use a queue for isolation.', ranges: [[6, 11]] },
          },
        ],
        decisions: [
          {
            ...identity,
            kind: 'decision',
            status: 'decided',
            text: 'Keep queue isolation',
            cited: false,
            turns: [],
            score: 9,
          },
        ],
      },
    }),
  );
  await openLocal(page, { width: 390, height: 844 });
  const search = page.getByRole('searchbox', { name: /Search session/ });
  await search.fill('queue');
  await expect.poll(() => allTimeRequested).toBe(true);
  await expect(page.getByRole('status', { name: 'Loading', exact: true })).toBeVisible();
  await expect(page.getByRole('table')).toBeHidden();
  releaseList();
  await expect(page.getByText('Search index building; using browser search.')).toBeVisible();
  ready = true;
  const decision = page.getByRole('button', { name: /Keep queue isolation/ });
  await expect(decision).toBeVisible();
  await expect(page.getByText('All time (search)', { exact: false })).toBeVisible();
  await expect(page.getByRole('table').getByText('prompt, turn 12:', { exact: false })).toBeVisible();
  await decision.focus();
  await expect(decision).toBeFocused();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.route('**/qa-axe.js', (route) =>
    route.fulfill({
      path: createRequire(import.meta.url).resolve('axe-core/axe.min.js'),
      contentType: 'application/javascript',
    }),
  );
  await page.addScriptTag({ url: '/qa-axe.js' });
  const violations = await page.evaluate(async () => {
    const result = await window.axe.run(document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
      rules: { 'color-contrast': { enabled: false } },
    });
    return result.violations.map(({ id, help }) => ({ id, help }));
  });
  expect(violations).toEqual([]);
  await step(page, 'ranked-narrow');
  await decision.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
  await search.fill('"queue"');
  ready = false;
  await expect(page.getByText('This search needs the index, which is still building.')).toBeVisible();
  await expect(page.getByRole('table')).toBeHidden();
});
