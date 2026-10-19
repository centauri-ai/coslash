// Build first, then run: node --test test/local-dialog.browser.mjs
// PLAYWRIGHT_MODULE may point to an existing Playwright installation.
import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { preview } from 'vite';

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || 'playwright');
let server;
let browser;
before(async () => {
  server = await preview({
    root: fileURLToPath(new URL('..', import.meta.url)),
    configFile: false,
    preview: { host: '127.0.0.1', port: 0 },
  });
  browser = await chromium.launch({ headless: true });
});
after(async () => {
  await browser?.close();
  await new Promise((resolve) => server.httpServer.close(resolve));
});

async function fixture(width, theme) {
  const page = await browser.newPage({ viewport: { width, height: 900 } });
  page.setDefaultTimeout(5000);
  const now = Date.now();
  const sessions = Array.from({ length: 24 }, (_, i) => ({
    sourceId: 'local',
    agent: 'codex',
    id: `p38-${i}`,
    name: `Reading session ${i}`,
    cwd: `/workspace/project-${i}`,
    repo: `project-${i}`,
    mtime: now,
    revision: now,
    turns: 8,
    contextTokens: 1500,
    compactions: 0,
    status: null,
    tokens: {},
    cost: 0,
    unpricedModels: [],
    subagents: [],
    files: 0,
    durationMs: 1000,
    entrypoint: 'codex',
    synthesis: null,
    synthesisPending: false,
    commands: [],
    commits: [],
    todos: [],
    digest: [],
    fileEdits: [],
    eligibleForAggregates: true,
    displayStale: false,
    toolUses: 0,
    errors: 0,
    prs: 0,
  }));
  let settings = {
    settings: {
      $schema: 'x',
      version: 1,
      synthesis: { enabled: false, backend: '', model: '' },
      appearance: { theme },
      launch: { terminal: 'terminal' },
    },
    persisted: false,
    valid: true,
    options: { synthesisBackends: [], terminals: [], reviewers: [] },
  };
  const saves = [];
  let failSave = false;
  let pendingSave;
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url());
    let body;
    switch (url.pathname) {
      case '/api/settings':
        if (route.request().method() === 'PUT') {
          saves.push(route.request().postDataJSON().settings);
          await pendingSave;
          if (failSave) return route.fulfill({ status: 500, body: 'Could not save settings' });
          settings = { ...settings, settings: saves.at(-1), persisted: true };
        }
        body = settings;
        break;
      case '/api/sessions':
        body = { sessions, machines: [{ sourceId: 'local', label: 'Local', state: 'ok', complete: true }] };
        break;
      case '/api/directed-handoffs':
        body = { handoffs: [] };
        break;
      case '/api/session-detail':
        body = {
          session: sessions[0],
          sourceId: 'local',
          agent: 'codex',
          sessionId: sessions[0].id,
          revision: String(now),
        };
        break;
      case '/api/synthesis':
        body = { synthesis: null, synthesisPending: false };
        break;
      default:
        return route.fulfill({ status: 404, json: {} });
    }
    await route.fulfill({ json: body });
  });
  await page.goto(server.resolvedUrls.local[0]);
  await page.getByText('Reading session 0', { exact: true }).waitFor();
  return {
    page,
    saves,
    holdSave: () => {
      let release;
      pendingSave = new Promise((resolve) => {
        release = resolve;
      });
      return release;
    },
    setFailSave: (value) => {
      failSave = value;
    },
  };
}

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 390]) {
    test(`synthesis stays optional, ${theme}, ${width}px`, async () => {
      const { page, saves, setFailSave } = await fixture(width, theme);
      try {
        await page.getByText('Reading session 0', { exact: true }).click();
        await page.getByRole('dialog', { name: 'Reading session 0', exact: true }).waitFor();
        assert.equal(
          await page.getByRole('dialog', { name: 'Choose how coSlash handles synthesis' }).count(),
          0,
        );
        await page.keyboard.press('Escape');
        await page.getByRole('dialog').waitFor({ state: 'hidden' });
        const prompt = page.getByRole('button', { name: 'Choose synthesis settings', exact: true });
        await prompt.click();
        const dialog = page.getByRole('dialog', { name: 'Choose how coSlash handles synthesis' });
        await dialog.waitFor();
        await page.keyboard.press('Escape');
        await dialog.waitFor({ state: 'hidden' });
        assert.equal(saves.length, 0, 'Escape keeps the default Off without saving');
        await prompt.click();
        await dialog.getByRole('button', { name: 'Close', exact: true }).click();
        await dialog.waitFor({ state: 'hidden' });
        assert.equal(saves.length, 0, 'Close keeps the default Off without saving');
        await prompt.click();
        if (process.env.SCREENSHOT_DIR) {
          await page.screenshot({
            animations: 'disabled',
            path: `${process.env.SCREENSHOT_DIR}/p38-${theme}-${width}.png`,
          });
        }
        const bounds = await dialog.boundingBox();
        assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width);
        setFailSave(true);
        await dialog.getByRole('button', { name: 'Save settings', exact: true }).click();
        await dialog.getByRole('alert').waitFor();
        setFailSave(false);
        await dialog.getByRole('button', { name: 'Save settings', exact: true }).click();
        await page.getByRole('dialog').waitFor({ state: 'hidden' });
        assert.equal(saves.at(-1).synthesis.enabled, false);
        await prompt.waitFor({ state: 'hidden' });
      } finally {
        await page.close();
      }
    });
    test(`group bands do not cover rows, ${theme}, ${width}px`, async () => {
      const { page } = await fixture(width, theme);
      try {
        await page.getByRole('button', { name: /Show .* more/ }).click();
        await page.locator('table').evaluate((table) => {
          table.parentElement.scrollTop = 42;
        });
        const overlap = await page.locator('table').evaluate((table) => {
          const band = table.querySelector('tbody th').getBoundingClientRect();
          const row = table.querySelector('tbody tr:nth-child(2)').getBoundingClientRect();
          return band.bottom - row.top;
        });
        assert.ok(overlap <= 1, `group header covers ${overlap}px of the first row`);
        if (process.env.SCREENSHOT_DIR) {
          await page.screenshot({
            animations: 'disabled',
            path: `${process.env.SCREENSHOT_DIR}/p39-${theme}-${width}.png`,
          });
        }
      } finally {
        await page.close();
      }
    });
  }
}

test('a dismissed save cannot close a reopened dialog', async () => {
  const { page, holdSave } = await fixture(1440, 'light');
  try {
    const prompt = page.getByRole('button', { name: 'Choose synthesis settings', exact: true });
    await prompt.click();
    const release = holdSave();
    const request = page.waitForRequest((request) => request.method() === 'PUT');
    await page.getByRole('button', { name: 'Save settings', exact: true }).click();
    await request;
    await page.keyboard.press('Escape');
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    await prompt.click();
    release();
    await page.getByRole('dialog').getByText('Saved', { exact: true }).waitFor();
    assert.equal(await page.getByRole('dialog').count(), 1);
  } finally {
    await page.close();
  }
});
