import { expect, test } from '@playwright/test';
import { checkLocal, openLocal, step } from './helpers.mjs';

test.beforeEach(async ({ request }) => {
  await checkLocal(request);
});

test('session list shows synthetic sessions', async ({ page }) => {
  await openLocal(page);
  await step(page, 'opened');
  await page.getByRole('button', { name: 'All time' }).click();
  await expect(page.getByRole('strong').filter({ hasText: /^\d+ sessions?$/ })).toBeVisible();
  const rows = page
    .getByRole('table')
    .getByRole('row')
    .filter({ has: page.getByRole('button', { name: /^Synthetic: / }) });
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeGreaterThanOrEqual(1);

  const filters = page.getByRole('region', { name: 'Filters' });
  await expect(filters.getByRole('button', { name: 'Claude Code' })).toBeVisible();
  await expect(filters.getByRole('button', { name: 'Codex' })).toBeVisible();
  await expect(filters.getByRole('button', { name: 'OpenCode' })).toBeVisible();
  await expect(filters.getByRole('button', { name: 'Cursor' })).toBeVisible();
  await step(page, 'sessions-listed-all-time');

  await page.getByRole('button', { name: 'Board' }).click();
  await expect(page.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByText('Loading board')).toBeHidden();
  await expect(page.getByText(/^Synthetic: /).first()).toBeVisible();
  await step(page, 'board-view');
});
