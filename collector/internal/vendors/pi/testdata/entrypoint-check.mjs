import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, copyFileSync, readdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import assert from 'node:assert/strict';

const release = process.env.PI_TEST_RELEASE_DIR;
assert.ok(release, 'Set PI_TEST_RELEASE_DIR to an installed verified release containing node_modules');
const pkg = path.join(release, 'node_modules/@earendil-works/pi-coding-agent');
const root = mkdtempSync(path.join(tmpdir(), 'coslash-pi-entrypoint-'));
const agent = path.join(root, 'agent');
mkdirSync(path.join(agent, 'extensions'), { recursive: true });
copyFileSync(new URL('../coslash-extension.ts', import.meta.url), path.join(agent, 'extensions/coslash-extension.ts'));
writeFileSync(path.join(agent, 'settings.json'), JSON.stringify({ compaction: { enabled: false } }));
const provider = path.join(root, 'offline-provider.ts');
writeFileSync(provider, readFileSync(new URL('./offline-provider.ts', import.meta.url), 'utf8').replace('__PI_AI_EVENT_STREAM__', pathToFileURL(path.join(release, 'node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js')).href));
const env = { ...process.env, PI_CODING_AGENT_DIR: agent, COSLASH_HOME: path.join(root, 'coslash'), XDG_CONFIG_HOME: path.join(root, 'xdg-config'), XDG_DATA_HOME: path.join(root, 'xdg-data') };
delete env.COSLASH_PI_ENTRYPOINT;
delete env.COSLASH_PI_HANDOFF_FILE;
delete env.COSLASH_PI_READY;
delete env.PI_TEST_REQUESTS;
delete env.COSLASH_TEST_UNSUPPORTED_PLATFORM;
const history = () => {
  try { return readdirSync(path.join(env.COSLASH_HOME, 'pi-history')).map(name => JSON.parse(readFileSync(path.join(env.COSLASH_HOME, 'pi-history', name), 'utf8'))); }
  catch { return []; }
};
async function run(file, args, input, declared, expected, unsupported = false, overrides = {}) {
  const childEnv = { ...env, ...overrides };
  if (declared) childEnv.COSLASH_PI_ENTRYPOINT = declared;
  if (unsupported) childEnv.COSLASH_TEST_UNSUPPORTED_PLATFORM = "linux";
  const child = spawn(process.execPath, [file, ...args], { cwd: root, env: childEnv });
  let diagnostic = '';
  child.stdout.resume();
  child.stderr.on('data', data => diagnostic += data);
  const timer = setTimeout(() => child.kill('SIGKILL'), 20000);
  child.stdin.end(input);
  try {
    const code = await new Promise((resolve, reject) => { child.on('error', reject); child.on('exit', resolve); });
    assert.equal(code, 0, diagnostic);
    const records = history().filter(item => item.record.pid === child.pid);
    if (unsupported) { assert.equal(records.length, 0, 'unsupported platform published runtime evidence'); return; }
    assert.ok(records.length, `Missing runtime evidence: ${diagnostic}`);
    for (const item of records) assert.equal(item.record.entrypoint, expected);
    return records;
  } finally { clearTimeout(timer); child.kill('SIGKILL'); }
}
const common = ['--no-tools', '-e', provider, '--provider', 'coslash-probe', '--model', 'probe', '--session-dir', path.join(root, 'sessions')];
const cli = path.join(pkg, 'dist/bundle/cli.js');
const original = await run(cli, [...common, '--mode', 'rpc'], JSON.stringify({ type: 'prompt', message: 'offline mode coverage' }) + '\n', 'pi-sdk', 'pi-rpc');
const transcript = original[0].record.transcriptPath;
const resumed = await run(cli, [...common, '--session', transcript, '--print'], 'offline resume coverage', undefined, 'pi-print');
for (const item of resumed) {
  assert.equal(item.record.sessionId, original[0].record.sessionId, 'resume must reopen the exact session');
  assert.equal(item.record.transcriptPath, transcript, 'resume must preserve its resolved path');
}
assert.ok(readFileSync(transcript, 'utf8').includes('offline resume coverage'), 'resume prompt must reach the selected transcript');

const handoff = path.join(root, 'handoff-context');
const requests = path.join(root, 'provider-requests.jsonl');
const notes = "prior-session context: quote ' dollar $ backtick `\nsecond line";
writeFileSync(handoff, notes, { mode: 0o600 });
const fresh = await run(cli, [...common, '--print'], 'offline fresh handoff coverage', undefined, 'pi-print', false, {
  COSLASH_PI_HANDOFF_FILE: handoff,
  PI_TEST_REQUESTS: requests,
});
assert.notEqual(fresh[0].record.sessionId, original[0].record.sessionId, 'handoff must start a fresh session');
const providerRequests = readFileSync(requests, 'utf8').trim().split('\n').map(line => JSON.parse(line));
assert.ok(providerRequests.length, 'handoff must reach the provider');
for (const request of providerRequests) assert.ok(request.messages.some(message => message.role === 'system' && typeof message.content === 'string' && message.content.includes(notes)), 'handoff context must reach the provider unchanged');
const promptFile = path.join(root, "prompt ' with spaces.txt");
const filePrompt = 'offline private file prompt';
writeFileSync(promptFile, filePrompt, { mode: 0o600 });
const sentFile = await run(cli, [...common, '--print', '--', '@' + promptFile], '', undefined, 'pi-print');
assert.ok(readFileSync(sentFile[0].record.transcriptPath, 'utf8').includes(filePrompt), '@file prompt must reach Pi without text on argv');
await run(cli, [...common, '--print'], 'offline print coverage', undefined, 'pi-print');
await run(cli, [...common, '--mode', 'json'], 'offline JSON coverage', undefined, 'pi-json');
await run(cli, common, 'offline implicit piped print coverage', undefined, 'pi-print');
const rpc = path.join(pkg, 'dist/bundle/rpc-entry.js');
if (existsSync(rpc)) await run(rpc, common, JSON.stringify({ type: 'prompt', message: 'offline RPC launcher coverage' }) + '\n', undefined, 'pi-rpc');

const sdk = path.join(root, 'sdk.mjs');
writeFileSync(sdk, `
import {createAgentSession, ModelRuntime, DefaultResourceLoader, SessionManager} from ${JSON.stringify(pathToFileURL(path.join(pkg, 'dist/index.js')).href)};
if (process.env.COSLASH_TEST_UNSUPPORTED_PLATFORM) Object.defineProperty(process, 'platform', { value: 'linux' });
const runtime = await ModelRuntime.create();
const loader = new DefaultResourceLoader({cwd:process.cwd(), agentDir:process.env.PI_CODING_AGENT_DIR, noExtensions:true, noSkills:true, noPromptTemplates:true, noContextFiles:true, additionalExtensionPaths:[${JSON.stringify(path.join(agent,'extensions/coslash-extension.ts'))},${JSON.stringify(provider)}]});
await loader.reload();
const {session} = await createAgentSession({cwd:process.cwd(), modelRuntime:runtime, resourceLoader:loader, sessionManager:SessionManager.create(process.cwd(),${JSON.stringify(path.join(root,'sdk-sessions'))}), noTools:true});
try {
  await session.bindExtensions({});
  await session.setModel(runtime.getModel('coslash-probe','probe'));
  await session.prompt('offline SDK coverage');
} finally { session.dispose(); }
`);
await run(sdk, [], '', undefined, undefined);
await run(sdk, [], '', 'pi-sdk', 'pi-sdk');
await run(sdk, [], '', 'pi-sdk', undefined, true);
console.log('PASS native exact-session resume, fresh handoff context, @file prompt, RPC, print, JSON, piped print, RPC launcher, SDK unknown and declared SDK, unsupported-platform extension no-op; no paid requests');
console.log('isolated root: ' + root);
