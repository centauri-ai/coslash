import { expect, test } from '@playwright/test';
import { checkLocal, openLocal, step } from './helpers.mjs';

test.beforeEach(async ({ request }) => {
  await checkLocal(request);
});

test('search and agent filter change the list', async ({ page }) => {
  await openLocal(page);
  await page.getByRole('button', { name: 'All time' }).click();
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
