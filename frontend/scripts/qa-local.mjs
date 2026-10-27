// Start an already-built Local on a synthetic home and print the env vars for `npm run qa:journeys`.
// Build first: make -C ../collector release. Stop with Ctrl-C.
import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const collector = path.resolve(import.meta.dirname, '../../collector');
const work = mkdtempSync(path.join(tmpdir(), 'coslash-qa-'));
const home = path.join(work, 'home');
const coslashHome = path.join(work, 'coslash-home');

const made = spawnSync(
  'go',
  [
    'run',
    './cmd/synthetic-home',
    '--out',
    home,
    '--seed',
    '1',
    '--now',
    new Date().toISOString(),
    '--sessions-per-agent',
    '3',
  ],
  { cwd: collector, stdio: 'inherit' },
);
if (made.status !== 0) process.exit(1);

const local = spawn(path.join(collector, 'bin/coslash'), ['--no-open', '--port', '0'], {
  stdio: 'inherit',
  env: {
    ...process.env,
    HOME: home,
    USERPROFILE: home,
    XDG_CONFIG_HOME: path.join(home, '.config'),
    XDG_DATA_HOME: path.join(home, '.local/share'),
    COSLASH_HOME: coslashHome,
  },
});
const stop = () => {
  local.kill('SIGTERM');
  rmSync(work, { recursive: true, force: true });
};
process.on('SIGINT', () => process.exit(0));
process.on('exit', stop);
local.on('exit', () => process.exit(1));

for (let i = 0; i < 100; i++) {
  try {
    const { baseURL } = JSON.parse(readFileSync(path.join(coslashHome, 'runtime.json'), 'utf8'));
    const token = readFileSync(path.join(coslashHome, 'token'), 'utf8').trim();
    console.log(`\nexport COSLASH_LOCAL_URL=${baseURL} COSLASH_LOCAL_TOKEN=${token}\nCtrl-C stops Local.`);
    break;
  } catch {
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
}
await new Promise(() => {});
