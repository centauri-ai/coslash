import { expect, test } from '@playwright/test';
import { checkLocal, openLocal, step } from './helpers.mjs';

test.beforeEach(async ({ request }) => {
  await checkLocal(request);
});

test('session detail dialog shows the session', async ({ page }) => {
  await openLocal(page);
  await page.getByRole('button', { name: 'All time' }).click();
  const first = page
    .getByRole('table')
    .getByRole('button', { name: /^Synthetic: / })
    .first();
  await expect(first).toBeVisible();
  const title = ((await first.textContent()) ?? '').trim();
  await step(page, 'list');

  await first.click();
  const dialog = page.getByRole('dialog', { name: title, exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Loading exact session details')).toBeHidden();
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeVisible();
  await expect(dialog).toContainText(/synthetic/i);
  await step(page, 'detail-open');

  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(first).toBeVisible();
  await step(page, 'detail-closed');
});
