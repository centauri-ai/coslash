import { expect, test } from '@playwright/test';
import { acceptFirstRunSettings, checkLocal, openLocal, step } from './helpers.mjs';

test.beforeEach(async ({ request }) => {
  await checkLocal(request);
});

async function expectNoHorizontalOverflow(page) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow, 'page scrolls horizontally').toBeLessThanOrEqual(0);
}

test('narrow 390px layout keeps search and rows reachable', async ({ page }) => {
  await openLocal(page, { width: 390, height: 844 });
  await page.getByRole('button', { name: 'All time' }).click();
  await step(page, 'narrow-list');
  const search = page.getByRole('searchbox', { name: /Search session/ });
  await expect(search).toBeVisible();
  const title = page
    .getByRole('table')
    .getByRole('button', { name: /^Synthetic: / })
    .first();
  await expect(title).toBeVisible();
  const box = await title.boundingBox();
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
  await expectNoHorizontalOverflow(page);

  await title.click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Loading exact session details')).toBeHidden();
  await expect(dialog).toContainText(/synthetic/i);
  await step(page, 'narrow-detail');
  const dialogBox = await dialog.boundingBox();
  expect(dialogBox.x).toBeGreaterThanOrEqual(0);
  expect(dialogBox.x + dialogBox.width).toBeLessThanOrEqual(390);
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeInViewport();
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toBeHidden();
});

test('dark theme list is readable', async ({ page }) => {
  await openLocal(page);
  const dark = page.getByRole('button', { name: 'Dark theme' });
  if (await dark.isDisabled()) {
    // A new Local home keeps the theme toggle off until settings are saved once.
    // Synthetic sessions do not trigger the synthesis prompt, so save the current settings through the API.
    await acceptFirstRunSettings(page.request);
    await page.reload();
    await expect(page.getByRole('table')).toBeVisible();
  }
  await expect(dark).toBeEnabled();
  try {
    await dark.click();
    await expect(dark).toHaveAttribute('aria-pressed', 'true');
    await expect(page.getByRole('button', { name: 'Light theme' })).toHaveAttribute('aria-pressed', 'false');
    const luminance = (value) => {
      const [r, g, b] = value.match(/\d+(\.\d+)?/g).map(Number);
      return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
    };
    const color = (selector, property) =>
      page.evaluate(([s, p]) => getComputedStyle(document.querySelector(s))[p], [selector, property]);
    await expect.poll(async () => luminance(await color('body', 'backgroundColor'))).toBeLessThan(0.3);
    await expect.poll(async () => luminance(await color('table tbody button', 'color'))).toBeGreaterThan(0.6);
    await step(page, 'dark-list');

    await page.setViewportSize({ width: 390, height: 844 });
    await expectNoHorizontalOverflow(page);
    await step(page, 'dark-narrow-list');
  } finally {
    // The theme is saved in Local, so restore it for the other journeys.
    await page.getByRole('button', { name: 'Light theme' }).click();
  }
});
