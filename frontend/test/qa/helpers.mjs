import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { expect, test } from '@playwright/test';

export function evidenceDir() {
  return process.env.QA_EVIDENCE_DIR || path.join(os.tmpdir(), 'coslash-qa-evidence');
}

function harnessError(message) {
  return new Error(`harness: ${message}`);
}

// Throws "harness: ..." when Local is not usable. Call it first in every journey.
export async function checkLocal(request) {
  const token = process.env.COSLASH_LOCAL_TOKEN;
  if (!process.env.COSLASH_LOCAL_URL) throw harnessError('COSLASH_LOCAL_URL is not set');
  if (!token) throw harnessError('COSLASH_LOCAL_TOKEN is not set');
  let response;
  try {
    response = await request.get('/api/settings', { headers: { 'X-Coslash-Token': token }, timeout: 10_000 });
  } catch (error) {
    throw harnessError(`Local did not respond at ${process.env.COSLASH_LOCAL_URL}: ${error.message}`);
  }
  if (!response.ok()) throw harnessError(`GET /api/settings returned ${response.status()}`);
  let sessions;
  try {
    sessions = await request.get('/api/sessions', {
      headers: { 'X-Coslash-Token': token },
      timeout: 10_000,
    });
  } catch (error) {
    throw harnessError(`Local did not respond at ${process.env.COSLASH_LOCAL_URL}: ${error.message}`);
  }
  if (!sessions.ok()) throw harnessError(`GET /api/sessions returned ${sessions.status()}`);
  const body = await sessions.json();
  if (!Array.isArray(body) || body.length === 0) {
    throw harnessError('Local has no sessions; start it on the synthetic home');
  }
}

// Saves the current settings unchanged. This marks the settings as persisted, which enables the theme toggle.
export async function acceptFirstRunSettings(request) {
  const headers = { 'X-Coslash-Token': process.env.COSLASH_LOCAL_TOKEN };
  const current = await request.get('/api/settings', { headers });
  const { settings } = await current.json();
  const saved = await request.put('/api/settings', { headers, data: { settings } });
  if (!saved.ok()) throw harnessError(`PUT /api/settings returned ${saved.status()}`);
}

// Local reads the token from the URL fragment (#t=...), keeps it in sessionStorage, and sends it as X-Coslash-Token.
// The first apiFetch call removes the fragment, so open the page once per browser context.
export async function openLocal(page, { width = 1280, height = 800, theme } = {}) {
  await page.setViewportSize({ width, height });
  if (theme) await page.emulateMedia({ colorScheme: theme });
  await page.goto(`/#t=${encodeURIComponent(process.env.COSLASH_LOCAL_TOKEN)}`);
  await expect(page.getByRole('table')).toBeVisible();
}

export async function step(page, name) {
  const info = test.info();
  const slug = info.titlePath
    .slice(1)
    .join('-')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '');
  const dir = path.join(evidenceDir(), slug);
  const number = info.annotations.filter((a) => a.type === 'qa-step').length + 1;
  info.annotations.push({ type: 'qa-step', description: name });
  if (number === 1) fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  await page.waitForLoadState('networkidle', { timeout: 5_000 }).catch(() => {});
  const file = `${String(number).padStart(2, '0')}-${name.replace(/[^a-z0-9]+/gi, '-').toLowerCase()}.png`;
  await page.screenshot({ path: path.join(dir, file), fullPage: true, animations: 'disabled' });
}
