import path from 'node:path';
import { defineConfig } from '@playwright/test';
import { evidenceDir } from './helpers.mjs';

const reporters = [['list']];
if (process.env.QA_REPORT_FILE) reporters.push(['json', { outputFile: process.env.QA_REPORT_FILE }]);

export default defineConfig({
  testDir: '.',
  testMatch: '*.qa.mjs',
  outputDir: path.join(evidenceDir(), 'playwright-output'),
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: reporters,
  use: {
    baseURL: process.env.COSLASH_LOCAL_URL,
    headless: true,
    actionTimeout: 10_000,
    navigationTimeout: 20_000,
    trace: 'retain-on-failure',
    screenshot: 'off',
  },
});
